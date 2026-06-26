package pdfdisassembler

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"iter"
	"os"
	"sort"
	"strconv"

	"github.com/speedata/pdfdisassembler/internal/lex"
)

// DefaultMaxStreamSize is the per-stream decoded-size cap Open uses by default.
const DefaultMaxStreamSize int64 = 16 << 20

// Option configures a Reader at Open time.
type Option func(*Reader)

// WithMaxStreamSize sets the per-stream decoded-size cap; n <= 0 disables it.
// Applied before parsing, so it also bounds streams decoded during Open.
func WithMaxStreamSize(n int64) Option {
	return func(r *Reader) { r.MaxStreamSize = n }
}

// Reader is a parsed PDF document. It is not safe for concurrent use.
type Reader struct {
	src      io.ReadSeeker
	closer   io.Closer
	buf      []byte // entire file contents
	version  string
	xref     map[Reference]xrefEntry
	trailer  *Dict
	catalog  *Dict
	info     *Dict
	infoLoad bool

	// MaxStreamSize caps each stream's decoded size; <= 0 disables it. Setting
	// it after Open misses Open-time (xref/object) streams; use WithMaxStreamSize.
	MaxStreamSize int64

	// Encryption.
	encrypt *encryptCtx

	// Resolution caches.
	objCache map[Reference]Object
	// resolveStack guards against indirect-reference cycles.
	resolveStack map[Reference]struct{}

	// Page-tree cache, populated lazily by loadPages.
	pages       []*Page
	pagesLoaded bool
	pagesErr    error
}

// xrefEntry describes a single in-use object.
type xrefEntry struct {
	// kind is 1 for in-file objects (Offset), 2 for compressed objects
	// (ObjStmNum + Index).
	kind       uint8
	offset     int64
	objStmNum  int
	objStmIdx  int
	generation int
}

// Open parses a PDF from rs. rs must remain valid for the lifetime of the
// returned Reader.
func Open(rs io.ReadSeeker, opts ...Option) (*Reader, error) {
	if _, err := rs.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("pdfdisassembler: seek: %w", err)
	}
	buf, err := io.ReadAll(rs)
	if err != nil {
		return nil, fmt.Errorf("pdfdisassembler: read: %w", err)
	}
	r := &Reader{
		src:           rs,
		buf:           buf,
		xref:          map[Reference]xrefEntry{},
		objCache:      map[Reference]Object{},
		resolveStack:  map[Reference]struct{}{},
		MaxStreamSize: DefaultMaxStreamSize,
	}
	for _, opt := range opts {
		opt(r)
	}
	if err := r.parseHeader(); err != nil {
		return nil, err
	}
	if err := r.parseXref(); err != nil {
		return nil, err
	}
	if err := r.initEncrypt(); err != nil {
		return nil, err
	}
	return r, nil
}

// OpenFile opens path and parses it as a PDF. The file stays open until
// Reader.Close is called.
func OpenFile(path string, opts ...Option) (*Reader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	r, err := Open(f, opts...)
	if err != nil {
		f.Close()
		return nil, err
	}
	r.closer = f
	return r, nil
}

// Close releases the underlying resource. For Reader instances created via
// Open with a non-file ReadSeeker, Close is a no-op.
func (r *Reader) Close() error {
	if r.closer != nil {
		return r.closer.Close()
	}
	return nil
}

// Version returns the PDF version declared in the file header (e.g. "1.7"
// or "2.0"). If the catalog declares a /Version entry that exceeds the
// header version, the catalog value wins (per spec).
func (r *Reader) Version() string {
	if r.catalog != nil {
		if n, ok := r.catalog.Name("Version"); ok {
			s := string(n)
			if s > r.version {
				return s
			}
		}
	}
	return r.version
}

// parseHeader reads the "%PDF-x.y" line. It tolerates up to 1024 leading
// bytes of garbage (some producers emit MIME prologues).
func (r *Reader) parseHeader() error {
	scan := r.buf
	limit := len(scan)
	if limit > 1024 {
		limit = 1024
	}
	idx := bytes.Index(scan[:limit], []byte("%PDF-"))
	if idx < 0 {
		return errors.New("pdfdisassembler: not a PDF (missing %PDF- header)")
	}
	rest := scan[idx+5:]
	end := 0
	for end < len(rest) && end < 16 {
		c := rest[end]
		if c == '\r' || c == '\n' || c == ' ' || c == '\t' {
			break
		}
		end++
	}
	r.version = string(rest[:end])
	return nil
}

// Catalog returns the document catalog dictionary.
func (r *Reader) Catalog() (*Dict, error) {
	if r.catalog != nil {
		return r.catalog, nil
	}
	if r.trailer == nil {
		return nil, errors.New("pdfdisassembler: no trailer")
	}
	root, ok := r.trailer.Get("Root")
	if !ok {
		return nil, errors.New("pdfdisassembler: trailer has no /Root")
	}
	d, err := r.ResolveDict(root)
	if err != nil {
		return nil, fmt.Errorf("pdfdisassembler: resolve catalog: %w", err)
	}
	r.catalog = d
	return d, nil
}

// Trailer returns the trailer dictionary.
func (r *Reader) Trailer() *Dict {
	return r.trailer
}

// Resolve follows an indirect reference. If obj is not a Reference, returns
// obj unchanged. Resolution is cached.
func (r *Reader) Resolve(obj Object) (Object, error) {
	ref, ok := obj.(Reference)
	if !ok {
		return obj, nil
	}
	if cached, ok := r.objCache[ref]; ok {
		return cached, nil
	}
	if _, on := r.resolveStack[ref]; on {
		return Null{}, fmt.Errorf("pdfdisassembler: reference cycle at %d %d R", ref.Number, ref.Generation)
	}
	r.resolveStack[ref] = struct{}{}
	defer delete(r.resolveStack, ref)

	entry, ok := r.xref[ref]
	if !ok {
		// Some xref tables omit the requested object. Per spec, missing
		// references resolve to null.
		r.objCache[ref] = Null{}
		return Null{}, nil
	}
	var v Object
	var err error
	switch entry.kind {
	case 1:
		v, err = r.readIndirectAt(entry.offset, ref)
	case 2:
		v, err = r.readCompressedObject(entry.objStmNum, entry.objStmIdx, ref)
	default:
		return nil, fmt.Errorf("pdfdisassembler: unknown xref entry kind for %d %d R", ref.Number, ref.Generation)
	}
	if err != nil {
		return nil, err
	}
	r.objCache[ref] = v
	return v, nil
}

// ResolveDict resolves obj to a *Dict; errors when obj is missing or is
// not a dictionary.
func (r *Reader) ResolveDict(obj Object) (*Dict, error) {
	v, err := r.Resolve(obj)
	if err != nil {
		return nil, err
	}
	if v == nil {
		return nil, errors.New("pdfdisassembler: nil object")
	}
	switch t := v.(type) {
	case *Dict:
		return t, nil
	case *Stream:
		return t.Dict, nil
	case Null:
		return nil, errors.New("pdfdisassembler: dictionary expected, got null")
	}
	return nil, fmt.Errorf("pdfdisassembler: dictionary expected, got %T", v)
}

// ResolveBool resolves obj to a bool; errors otherwise.
func (r *Reader) ResolveBool(obj Object) (bool, error) {
	v, err := r.Resolve(obj)
	if err != nil {
		return false, err
	}
	b, ok := v.(Bool)
	if !ok {
		return false, fmt.Errorf("pdfdisassembler: boolean expected, got %T", v)
	}
	return bool(b), nil
}

// ResolveInt resolves obj to an int64; errors otherwise.
func (r *Reader) ResolveInt(obj Object) (int64, error) {
	v, err := r.Resolve(obj)
	if err != nil {
		return 0, err
	}
	n, ok := v.(Integer)
	if !ok {
		return 0, fmt.Errorf("pdfdisassembler: integer expected, got %T", v)
	}
	return int64(n), nil
}

// ResolveArray resolves obj to an Array; errors otherwise.
func (r *Reader) ResolveArray(obj Object) (Array, error) {
	v, err := r.Resolve(obj)
	if err != nil {
		return nil, err
	}
	a, ok := v.(Array)
	if !ok {
		return nil, fmt.Errorf("pdfdisassembler: array expected, got %T", v)
	}
	return a, nil
}

// readIndirectAt parses the indirect object starting at offset. The object
// header (N G obj) is verified against the expected reference.
func (r *Reader) readIndirectAt(offset int64, expect Reference) (Object, error) {
	if offset < 0 || offset >= int64(len(r.buf)) {
		return nil, fmt.Errorf("pdfdisassembler: xref offset %d out of range", offset)
	}
	lx := lex.New(r.buf)
	lx.SetPos(int(offset))
	p := newParser(lx, r)

	// Read "N G obj" header.
	t1, err := p.next()
	if err != nil {
		return nil, err
	}
	t2, err := p.next()
	if err != nil {
		return nil, err
	}
	t3, err := p.next()
	if err != nil {
		return nil, err
	}
	if t1.Kind != lex.Integer || t2.Kind != lex.Integer ||
		t3.Kind != lex.Keyword || string(t3.Bytes) != "obj" {
		return nil, fmt.Errorf("pdfdisassembler: bad indirect header at %d (got %s %s %s)", offset, t1.Kind, t2.Kind, t3.Kind)
	}
	n, _ := strconv.Atoi(string(t1.Bytes))
	g, _ := strconv.Atoi(string(t2.Bytes))
	if n != expect.Number {
		return nil, fmt.Errorf("pdfdisassembler: indirect mismatch at %d: header %d %d, expected %d %d", offset, n, g, expect.Number, expect.Generation)
	}

	body, err := p.parseObject()
	if err != nil {
		return nil, fmt.Errorf("pdfdisassembler: parse body of %d %d R: %w", expect.Number, expect.Generation, err)
	}

	// Check for stream.
	t4, err := p.peek()
	if err == nil && t4.Kind == lex.Keyword && string(t4.Bytes) == "stream" {
		p.next()
		d, ok := body.(*Dict)
		if !ok {
			return nil, fmt.Errorf("pdfdisassembler: stream object %d %d R has non-dict body (%T)", expect.Number, expect.Generation, body)
		}
		length, err := r.streamLength(d)
		if err != nil {
			return nil, fmt.Errorf("pdfdisassembler: /Length for %d %d R: %w", expect.Number, expect.Generation, err)
		}
		raw, err := lx.ReadStreamData(int(length))
		if err != nil {
			return nil, fmt.Errorf("pdfdisassembler: read stream %d %d R: %w", expect.Number, expect.Generation, err)
		}
		// rawOffset is where the raw bytes begin in the file.
		rawStart := lx.Pos() - len(raw)
		return &Stream{
			Dict:          d,
			reader:        r,
			rawOffset:     int64(rawStart),
			rawLength:     int64(len(raw)),
			objNumber:     expect.Number,
			objGeneration: expect.Generation,
		}, nil
	}
	return body, nil
}

// streamLength resolves the /Length entry on a stream dict.
func (r *Reader) streamLength(d *Dict) (int64, error) {
	v, ok := d.Get("Length")
	if !ok {
		return 0, errors.New("missing /Length")
	}
	v, err := r.Resolve(v)
	if err != nil {
		return 0, err
	}
	n, ok := v.(Integer)
	if !ok {
		return 0, fmt.Errorf("/Length is %T, want integer", v)
	}
	if n < 0 {
		return 0, fmt.Errorf("/Length is negative: %d", n)
	}
	return int64(n), nil
}

// DocumentInfo returns the standard /Info dictionary entries as a value
// snapshot. Missing entries return zero values.
func (r *Reader) DocumentInfo() DocInfo {
	if !r.infoLoad {
		r.infoLoad = true
		if r.trailer != nil {
			if obj, ok := r.trailer.Get("Info"); ok {
				if d, err := r.ResolveDict(obj); err == nil {
					r.info = d
				}
			}
		}
	}
	var info DocInfo
	info.Custom = map[string]string{}
	if r.info == nil {
		return info
	}
	for k, v := range r.info.Iter() {
		resolved, err := r.Resolve(v)
		if err != nil {
			continue
		}
		s, ok := resolved.(String)
		if !ok {
			continue
		}
		decoded := decodeTextString(s)
		switch k {
		case "Title":
			info.Title = decoded
		case "Author":
			info.Author = decoded
		case "Subject":
			info.Subject = decoded
		case "Keywords":
			info.Keywords = decoded
		case "Creator":
			info.Creator = decoded
		case "Producer":
			info.Producer = decoded
		case "CreationDate":
			info.CreationDate = parseDate(decoded)
		case "ModDate":
			info.ModDate = parseDate(decoded)
		default:
			info.Custom[k] = decoded
		}
	}
	return info
}

// Objects iterates every live indirect object in the xref table.
func (r *Reader) Objects() iter.Seq[ObjectEntry] {
	return func(yield func(ObjectEntry) bool) {
		// Iterate in stable order: by object number.
		refs := make([]Reference, 0, len(r.xref))
		for ref := range r.xref {
			refs = append(refs, ref)
		}
		// The entry count is attacker-controlled, so this must stay O(n log n).
		sort.Slice(refs, func(i, j int) bool {
			if refs[i].Number != refs[j].Number {
				return refs[i].Number < refs[j].Number
			}
			return refs[i].Generation < refs[j].Generation
		})
		for _, ref := range refs {
			obj, err := r.Resolve(ref)
			if err != nil {
				continue
			}
			if !yield(ObjectEntry{Reference: ref, Object: obj}) {
				return
			}
		}
	}
}

// DecodeStream resolves obj to a stream and returns its decoded content.
func (r *Reader) DecodeStream(obj Object) ([]byte, error) {
	v, err := r.Resolve(obj)
	if err != nil {
		return nil, err
	}
	s, ok := v.(*Stream)
	if !ok {
		return nil, fmt.Errorf("pdfdisassembler: stream expected, got %T", v)
	}
	return s.Content()
}

const maxNameTreeDepth = 1000

// EmbeddedFiles returns the document's embedded files (PDF attachments) from
// the catalog's EmbeddedFiles name tree, in tree order. Returns nil when there
// are none.
func (r *Reader) EmbeddedFiles() []EmbeddedFile {
	cat, err := r.Catalog()
	if err != nil {
		return nil
	}
	names, ok := cat.Dict("Names")
	if !ok {
		return nil
	}
	root, ok := names.Dict("EmbeddedFiles")
	if !ok {
		return nil
	}
	var out []EmbeddedFile
	r.walkNameTree(root, map[Reference]struct{}{}, 0, &out)
	return out
}

// walkNameTree collects (name, /Filespec) pairs from a name-tree node. seen
// records already-visited /Kids references and depth bounds the descent, so a
// cyclic or pathologically deep /Kids graph can't loop or overflow the stack.
func (r *Reader) walkNameTree(node *Dict, seen map[Reference]struct{}, depth int, out *[]EmbeddedFile) {
	if node == nil || depth > maxNameTreeDepth {
		return
	}
	if kids, ok := node.Array("Kids"); ok {
		for _, kid := range kids {
			if ref, ok := kid.(Reference); ok {
				if _, dup := seen[ref]; dup {
					continue
				}
				seen[ref] = struct{}{}
			}
			if child, err := r.ResolveDict(kid); err == nil {
				r.walkNameTree(child, seen, depth+1, out)
			}
		}
	}
	if entries, ok := node.Array("Names"); ok {
		for i := 0; i+1 < len(entries); i += 2 {
			name, ok := entries[i].(String)
			if !ok {
				continue
			}
			if spec, err := r.ResolveDict(entries[i+1]); err == nil {
				*out = append(*out, EmbeddedFile{Name: string(name), Spec: spec})
			}
		}
	}
}
