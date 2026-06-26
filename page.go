package pdfdisassembler

import (
	"bytes"
	"errors"
	"fmt"
)

// maxPageTreeDepth bounds both the page-tree (/Kids) descent and the
// inheritance (/Parent) walk so a hostile or cyclic structure can't loop
// forever or overflow the stack.
const maxPageTreeDepth = 1000

// BoxName identifies one of the page boundary boxes (PDF 32000-1:2008
// §14.11.2).
type BoxName string

// The five page boundary boxes, in the spec's containment order (MediaBox is
// the largest, ArtBox the smallest).
const (
	MediaBox BoxName = "MediaBox"
	CropBox  BoxName = "CropBox"
	BleedBox BoxName = "BleedBox"
	TrimBox  BoxName = "TrimBox"
	ArtBox   BoxName = "ArtBox"
)

// boxNames is the canonical box list iterated by Page.Boxes.
var boxNames = []BoxName{MediaBox, CropBox, BleedBox, TrimBox, ArtBox}

// Rect is a PDF rectangle in default user-space units (points), normalised so
// LLX <= URX and LLY <= URY regardless of the corner order written in the file.
type Rect struct {
	LLX, LLY, URX, URY float64
}

// Width returns the rectangle's horizontal extent.
func (r Rect) Width() float64 { return r.URX - r.LLX }

// Height returns the rectangle's vertical extent.
func (r Rect) Height() float64 { return r.URY - r.LLY }

// Page is a handle to a single leaf page (/Type /Page) of the page tree. Its
// accessors resolve the inheritable attributes — boxes, /Rotate, /Resources —
// by walking the /Parent chain per PDF 32000-1:2008 §7.7.3.4. Obtain one via
// Reader.Page or Reader.Pages.
type Page struct {
	reader *Reader
	dict   *Dict
	index  int
}

// Index returns the page's 0-based position in display order.
func (p *Page) Index() int { return p.index }

// Dict returns the page's own dictionary, without inherited attributes
// flattened in. Use the Box, Rotation, and Resources accessors for values that
// may be inherited from an ancestor /Pages node.
func (p *Page) Dict() *Dict { return p.dict }

// PageCount returns the number of leaf pages in the document.
func (r *Reader) PageCount() (int, error) {
	pages, err := r.loadPages()
	if err != nil {
		return 0, err
	}
	return len(pages), nil
}

// Pages returns every leaf page in display (reading) order.
func (r *Reader) Pages() ([]*Page, error) {
	pages, err := r.loadPages()
	if err != nil {
		return nil, err
	}
	out := make([]*Page, len(pages))
	copy(out, pages)
	return out, nil
}

// Page returns the leaf page at the given 0-based index in display order.
func (r *Reader) Page(index int) (*Page, error) {
	pages, err := r.loadPages()
	if err != nil {
		return nil, err
	}
	if index < 0 || index >= len(pages) {
		return nil, fmt.Errorf("pdfdisassembler: page index %d out of range (%d pages)", index, len(pages))
	}
	return pages[index], nil
}

// loadPages walks the page tree once and caches the flat leaf-page list (and
// any error) for subsequent calls.
func (r *Reader) loadPages() ([]*Page, error) {
	if r.pagesLoaded {
		return r.pages, r.pagesErr
	}
	r.pagesLoaded = true
	r.pages, r.pagesErr = r.buildPages()
	return r.pages, r.pagesErr
}

func (r *Reader) buildPages() ([]*Page, error) {
	cat, err := r.Catalog()
	if err != nil {
		return nil, err
	}
	rootRef, ok := cat.Get("Pages")
	if !ok {
		return nil, errors.New("pdfdisassembler: catalog has no /Pages")
	}
	root, err := r.ResolveDict(rootRef)
	if err != nil {
		return nil, fmt.Errorf("pdfdisassembler: resolve /Pages: %w", err)
	}
	seen := map[Reference]struct{}{}
	if ref, ok := rootRef.(Reference); ok {
		seen[ref] = struct{}{}
	}
	var out []*Page
	r.collectPages(root, seen, 0, &out)
	return out, nil
}

// collectPages descends the page tree depth-first, appending each leaf page to
// out in display order. A node is treated as an intermediate /Pages node when
// it carries /Kids, otherwise as a leaf /Page — /Type is only a hint, since
// some producers omit it. seen guards against cyclic /Kids references and depth
// bounds the descent.
func (r *Reader) collectPages(node *Dict, seen map[Reference]struct{}, depth int, out *[]*Page) {
	if node == nil || depth > maxPageTreeDepth {
		return
	}
	kids, ok := node.Array("Kids")
	if !ok {
		// No resolvable /Kids array. A node that still looks like an
		// intermediate /Pages node — it declares /Type /Pages, /Count, or a
		// /Kids key that failed to resolve — is a broken or empty branch, not a
		// leaf page; emitting it would invent a phantom page.
		if node.Has("Kids") || node.Has("Count") {
			return
		}
		if t, ok := node.Name("Type"); ok && t == "Pages" {
			return
		}
		*out = append(*out, &Page{reader: r, dict: node, index: len(*out)})
		return
	}
	for _, kid := range kids {
		if ref, ok := kid.(Reference); ok {
			if _, dup := seen[ref]; dup {
				continue
			}
			seen[ref] = struct{}{}
		}
		child, err := r.ResolveDict(kid)
		if err != nil {
			continue
		}
		r.collectPages(child, seen, depth+1, out)
	}
}

// inherited walks the /Parent chain starting at the page dictionary and returns
// the resolved value of the first ancestor that carries key. ok is false when
// no ancestor defines it. Resolution is cached, so the same /Parent reference
// yields the same *Dict pointer — pointer identity (plus the depth bound)
// terminates a cyclic chain.
func (p *Page) inherited(key string) (Object, bool) {
	seen := map[*Dict]struct{}{}
	node := p.dict
	for depth := 0; node != nil && depth <= maxPageTreeDepth; depth++ {
		if _, dup := seen[node]; dup {
			return nil, false
		}
		seen[node] = struct{}{}
		if v, ok := node.resolved(key); ok {
			return v, true
		}
		parent, ok := node.Dict("Parent")
		if !ok {
			return nil, false
		}
		node = parent
	}
	return nil, false
}

// Box returns the named page boundary box. MediaBox and CropBox are resolved
// through inheritance along the /Parent chain; BleedBox, TrimBox and ArtBox are
// read from the page object only, since only MediaBox and CropBox carry the
// (Inheritable) marker in PDF 32000-1:2008 Table 30 (§14.11.2). ok is false
// when the box is not defined there, or its value is not a well-formed array of
// four numbers. No spec-default substitution (e.g. a missing box defaulting to
// CropBox) is applied.
func (p *Page) Box(name BoxName) (Rect, bool) {
	var v Object
	var ok bool
	if name == MediaBox || name == CropBox {
		v, ok = p.inherited(string(name))
	} else {
		v, ok = p.dict.resolved(string(name))
	}
	if !ok {
		return Rect{}, false
	}
	arr, ok := v.(Array)
	if !ok {
		return Rect{}, false
	}
	return rectFromArray(p.reader, arr)
}

// Boxes returns every boundary box defined for the page, keyed by name, with
// the same per-box inheritance rules as Box (MediaBox and CropBox may be
// inherited from an ancestor; BleedBox/TrimBox/ArtBox are page-level only).
// Boxes that are not defined are omitted; no spec-default substitution (e.g. a
// missing CropBox defaulting to MediaBox) is applied.
func (p *Page) Boxes() map[BoxName]Rect {
	out := map[BoxName]Rect{}
	for _, name := range boxNames {
		if rect, ok := p.Box(name); ok {
			out[name] = rect
		}
	}
	return out
}

// Rotation returns the page's clockwise display rotation in degrees, resolved
// through inheritance and normalised to one of 0, 90, 180, 270. A missing,
// non-integer, or non-multiple-of-90 /Rotate yields 0.
func (p *Page) Rotation() int {
	v, ok := p.inherited("Rotate")
	if !ok {
		return 0
	}
	n, ok := v.(Integer)
	if !ok {
		return 0
	}
	deg := int(n) % 360
	if deg < 0 {
		deg += 360
	}
	if deg%90 != 0 {
		return 0
	}
	return deg
}

// Resources returns the page's resource dictionary, resolved through
// inheritance along the /Parent chain. ok is false when neither the page nor
// any ancestor defines /Resources.
func (p *Page) Resources() (*Dict, bool) {
	v, ok := p.inherited("Resources")
	if !ok {
		return nil, false
	}
	d, ok := v.(*Dict)
	return d, ok
}

// ContentStreams returns the page's content streams in order. /Contents may be
// a single stream or an array of streams (PDF 32000-1:2008 §7.7.3.3); both are
// flattened to the underlying Stream objects. It returns nil (no error) when
// the page has no /Contents. Non-stream entries are skipped defensively.
func (p *Page) ContentStreams() ([]*Stream, error) {
	v, ok := p.dict.Get("Contents")
	if !ok {
		return nil, nil
	}
	resolved, err := p.reader.Resolve(v)
	if err != nil {
		return nil, fmt.Errorf("pdfdisassembler: resolve /Contents: %w", err)
	}
	var out []*Stream
	switch t := resolved.(type) {
	case *Stream:
		out = append(out, t)
	case Array:
		for _, e := range t {
			s, err := p.reader.Resolve(e)
			if err != nil {
				return nil, fmt.Errorf("pdfdisassembler: resolve /Contents entry: %w", err)
			}
			stm, ok := s.(*Stream)
			if !ok {
				// A missing entry resolves to Null, not an error; dropping it
				// would silently truncate the page's drawing instructions, so
				// fail loudly instead.
				return nil, fmt.Errorf("pdfdisassembler: /Contents array entry resolved to %T, want stream", s)
			}
			out = append(out, stm)
		}
	}
	return out, nil
}

// Content returns the page's decoded content: every content stream decoded via
// its filter chain and concatenated with a single newline between streams (a
// token cannot span a stream boundary, per PDF 32000-1:2008 §7.8.2). The result
// is the raw drawing-instruction byte stream; this library does not interpret
// it (see package contentstream for tokenisation).
func (p *Page) Content() ([]byte, error) {
	streams, err := p.ContentStreams()
	if err != nil {
		return nil, err
	}
	parts := make([][]byte, 0, len(streams))
	for _, s := range streams {
		data, err := s.Content()
		if err != nil {
			return nil, err
		}
		parts = append(parts, data)
	}
	return bytes.Join(parts, []byte{'\n'}), nil
}

// rectFromArray converts a 4-element PDF array [llx lly urx ury] to a
// normalised Rect. Entries may be Integer or Real and may be indirect
// references. ok is false for any other shape.
func rectFromArray(r *Reader, arr Array) (Rect, bool) {
	if len(arr) != 4 {
		return Rect{}, false
	}
	var v [4]float64
	for i, e := range arr {
		f, ok := numberValue(r, e)
		if !ok {
			return Rect{}, false
		}
		v[i] = f
	}
	rect := Rect{LLX: v[0], LLY: v[1], URX: v[2], URY: v[3]}
	if rect.LLX > rect.URX {
		rect.LLX, rect.URX = rect.URX, rect.LLX
	}
	if rect.LLY > rect.URY {
		rect.LLY, rect.URY = rect.URY, rect.LLY
	}
	return rect, true
}

// numberValue resolves obj and returns it as a float64 if it is an Integer or
// Real.
func numberValue(r *Reader, obj Object) (float64, bool) {
	resolved, err := r.Resolve(obj)
	if err != nil {
		return 0, false
	}
	switch n := resolved.(type) {
	case Integer:
		return float64(n), true
	case Real:
		return float64(n), true
	}
	return 0, false
}
