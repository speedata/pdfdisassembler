package pdfdisassembler

import (
	"iter"
	"time"
)

// Object is the sealed PDF object type. Concrete variants:
//
//	Name, Integer, Real, Bool, String, *Dict, Array, *Stream, Reference, Null
//
// Callers should type-switch or type-assert on the concrete type when they
// need a specific value. The Resolve* helpers on Reader perform the common
// dereference-and-assert pattern.
type Object interface {
	object()
}

// Name is a PDF name object, e.g. /Length, /Type. The leading slash is not
// stored.
type Name string

// Integer is a PDF integer object.
type Integer int64

// Real is a PDF real-number object.
type Real float64

// Bool is a PDF boolean object.
type Bool bool

// String is a PDF string object after parsing. The parser strips the
// literal-string parentheses or hex-string angle brackets and decodes
// escape sequences and hexadecimal pairs, but does not re-encode the
// bytes — they are whatever the producer wrote.
//
// For PDF text strings ("Title", "Subject", "Producer", and so on) use
// Dict.String or DocumentInfo, which apply the text-string decoding rules
// (PDFDocEncoding, UTF-16BE BOM, UTF-8 BOM).
type String []byte

// Array is a PDF array of objects.
type Array []Object

// Reference is an indirect object reference (e.g. "12 0 R").
type Reference struct {
	Number     int
	Generation int
}

// Null is the PDF null object.
type Null struct{}

func (Name) object()      {}
func (Integer) object()   {}
func (Real) object()      {}
func (Bool) object()      {}
func (String) object()    {}
func (Array) object()     {}
func (Reference) object() {}
func (Null) object()      {}
func (*Dict) object()     {}
func (*Stream) object()   {}

// Dict is a PDF dictionary that preserves insertion order during iteration.
type Dict struct {
	keys   []string
	values map[string]Object
	// reader is set by the parser so the dereferencing convenience methods
	// (Dict, Array-of-dicts walks) can follow indirect references. nil
	// when the dictionary was synthesised outside of a Reader.
	reader *Reader
}

// newDict returns an empty dictionary tied to r (may be nil during early
// parsing of trailer/xref dicts).
func newDict(r *Reader) *Dict {
	return &Dict{values: map[string]Object{}, reader: r}
}

// Len returns the number of entries in the dictionary.
func (d *Dict) Len() int {
	if d == nil {
		return 0
	}
	return len(d.keys)
}

// Get returns the raw object for key. The returned object may be a
// Reference; use Dict.Dict / Reader.Resolve if you need the resolved value.
func (d *Dict) Get(key string) (Object, bool) {
	if d == nil {
		return nil, false
	}
	v, ok := d.values[key]
	return v, ok
}

// Has reports whether key is present.
func (d *Dict) Has(key string) bool {
	if d == nil {
		return false
	}
	_, ok := d.values[key]
	return ok
}

// Keys returns the dictionary keys in insertion order.
func (d *Dict) Keys() []string {
	if d == nil {
		return nil
	}
	out := make([]string, len(d.keys))
	copy(out, d.keys)
	return out
}

// Iter returns an iterator over key/value pairs in insertion order.
func (d *Dict) Iter() iter.Seq2[string, Object] {
	return func(yield func(string, Object) bool) {
		if d == nil {
			return
		}
		for _, k := range d.keys {
			if !yield(k, d.values[k]) {
				return
			}
		}
	}
}

// set inserts or updates an entry, preserving insertion order on first set.
func (d *Dict) set(key string, value Object) {
	if _, ok := d.values[key]; !ok {
		d.keys = append(d.keys, key)
	}
	d.values[key] = value
}

// Name returns the Name value at key. If the value is a Reference, it is
// resolved first.
func (d *Dict) Name(key string) (Name, bool) {
	v, ok := d.resolved(key)
	if !ok {
		return "", false
	}
	n, ok := v.(Name)
	return n, ok
}

// Int returns the Integer value at key. If the value is a Reference, it is
// resolved first.
func (d *Dict) Int(key string) (int64, bool) {
	v, ok := d.resolved(key)
	if !ok {
		return 0, false
	}
	n, ok := v.(Integer)
	if !ok {
		return 0, false
	}
	return int64(n), true
}

// Bool returns the Bool value at key. If the value is a Reference, it is
// resolved first.
func (d *Dict) Bool(key string) (bool, bool) {
	v, ok := d.resolved(key)
	if !ok {
		return false, false
	}
	b, ok := v.(Bool)
	return bool(b), ok
}

// Array returns the Array value at key. If the value is a Reference, it is
// resolved first.
func (d *Dict) Array(key string) (Array, bool) {
	v, ok := d.resolved(key)
	if !ok {
		return nil, false
	}
	a, ok := v.(Array)
	return a, ok
}

// Dict returns the *Dict value at key. If the value is a Reference, it is
// resolved first.
func (d *Dict) Dict(key string) (*Dict, bool) {
	v, ok := d.resolved(key)
	if !ok {
		return nil, false
	}
	dd, ok := v.(*Dict)
	return dd, ok
}

// Stream returns the *Stream value at key. If the value is a Reference, it
// is resolved first.
func (d *Dict) Stream(key string) (*Stream, bool) {
	v, ok := d.resolved(key)
	if !ok {
		return nil, false
	}
	s, ok := v.(*Stream)
	return s, ok
}

// String returns the value at key as a Go string, decoded according to the
// PDF text-string rules (UTF-16BE BOM, UTF-8 BOM, otherwise PDFDocEncoding).
// If the value is a Reference, it is resolved first.
func (d *Dict) String(key string) (string, bool) {
	v, ok := d.resolved(key)
	if !ok {
		return "", false
	}
	s, ok := v.(String)
	if !ok {
		return "", false
	}
	return decodeTextString(s), true
}

// Bytes returns the raw bytes of a String value at key, without
// text-string decoding. Useful for byte strings (file identifiers, hashes).
// If the value is a Reference, it is resolved first.
func (d *Dict) Bytes(key string) ([]byte, bool) {
	v, ok := d.resolved(key)
	if !ok {
		return nil, false
	}
	s, ok := v.(String)
	if !ok {
		return nil, false
	}
	return []byte(s), true
}

// resolved returns the value at key, dereferencing once through the
// reader if the value is a Reference. If resolution fails, ok is false.
func (d *Dict) resolved(key string) (Object, bool) {
	v, ok := d.Get(key)
	if !ok {
		return nil, false
	}
	if ref, ok := v.(Reference); ok {
		if d.reader == nil {
			return nil, false
		}
		obj, err := d.reader.Resolve(ref)
		if err != nil {
			return nil, false
		}
		return obj, true
	}
	return v, true
}

// Stream is a stream object. The decoded content is produced by Content,
// which applies the declared filter chain (FlateDecode, ASCII85, …) and
// any document-level decryption, and caches the result.
type Stream struct {
	// Dict is the stream's parameter dictionary, e.g. /Length, /Filter.
	Dict *Dict
	// reader is the document the stream came from.
	reader *Reader
	// rawOffset is the byte offset in the underlying ReadSeeker where the
	// raw stream bytes begin (just after the "stream" keyword and EOL).
	rawOffset int64
	// rawLength is the raw byte count of the stream as declared by /Length.
	rawLength int64
	// objNumber, objGeneration identify the indirect object this stream
	// belongs to. Used for per-object decryption keys.
	objNumber     int
	objGeneration int
	// cache holds the decoded content after the first call to Content.
	cache    []byte
	cacheErr error
	cached   bool
}

// Content returns the decoded stream bytes. Filters and decryption are
// applied on the first call and the result is cached for subsequent calls.
func (s *Stream) Content() ([]byte, error) {
	if s.cached {
		return s.cache, s.cacheErr
	}
	b, err := s.reader.decodeStream(s)
	s.cache = b
	s.cacheErr = err
	s.cached = true
	return b, err
}

// RawLength returns the declared raw byte length of the stream.
func (s *Stream) RawLength() int64 {
	return s.rawLength
}

// ObjectEntry is yielded by Reader.Objects: an in-use indirect object plus
// its resolved value.
type ObjectEntry struct {
	Reference Reference
	Object    Object
}

// DocInfo is a value snapshot of the standard /Info dictionary entries.
// Missing entries are zero values. Custom carries any non-standard keys
// as raw decoded strings.
type DocInfo struct {
	Title        string
	Author       string
	Subject      string
	Keywords     string
	Creator      string
	Producer     string
	CreationDate time.Time
	ModDate      time.Time
	Custom       map[string]string
}
