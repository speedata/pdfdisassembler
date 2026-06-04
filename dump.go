package pdfdisassembler

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// DumpOptions controls Dump's behaviour.
type DumpOptions struct {
	// PreviewMaxBytes is the maximum number of decoded stream bytes shown
	// as the preview_utf8 field. Default 80. Set to -1 to disable.
	PreviewMaxBytes int
	// InlineStreamContent embeds the decoded stream as hex under the
	// "decoded.hex" field. Off by default — real PDFs produce huge
	// fixtures otherwise.
	InlineStreamContent bool
}

// Dump returns a deterministic JSON snapshot of r suitable for golden-file
// snapshot tests and human inspection.
//
// The output is tagged so every PDF value kind is unambiguous (Name vs.
// Text-String vs. Byte-String, Integer vs. Real, etc.). Indirect references
// are preserved as references, so the object graph is acyclic and diffs
// stay reviewable in PRs. Streams contribute metadata (raw_length, filter
// chain, decoded length, SHA-256, optional preview) but never their full
// content — see DumpOptions.InlineStreamContent if you need it.
//
// The output is intended to be byte-stable across runs; dictionary keys
// are emitted in PDF insertion order, objects in (Number, Generation)
// order.
func Dump(r *Reader, opts DumpOptions) ([]byte, error) {
	if opts.PreviewMaxBytes == 0 {
		opts.PreviewMaxBytes = 80
	}

	top := orderedMap{}
	top = append(top, orderedKV{"version", r.Version()})
	top = append(top, orderedKV{"xref_format", r.xrefFormat()})
	top = append(top, orderedKV{"encrypted", r.encrypt != nil})

	if r.trailer != nil {
		top = append(top, orderedKV{"trailer", dumpDictTagged(r.trailer, opts)})
	}

	objs := orderedMap{}
	for entry := range r.Objects() {
		key := fmt.Sprintf("%d %d", entry.Reference.Number, entry.Reference.Generation)
		objs = append(objs, orderedKV{key, dumpValue(entry.Object, opts)})
	}
	top = append(top, orderedKV{"objects", objs})

	raw, err := json.Marshal(top)
	if err != nil {
		return nil, fmt.Errorf("pdfdisassembler/dump: marshal: %w", err)
	}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, raw, "", "  "); err != nil {
		return nil, fmt.Errorf("pdfdisassembler/dump: indent: %w", err)
	}
	pretty.WriteByte('\n')
	// Unescape the HTML-safe sequences that json.Marshal emits by default.
	// PDFs are full of '<<' and '>>' (dict delimiters, hex strings), and
	// '&' shows up in metadata XML — readable diffs win over byte-pedantic
	// HTML-safety, which is irrelevant here. The transform is safe: these
	// escape sequences only appear inside JSON string literals, where the
	// unescaped form is equivalent.
	return unescapeHTMLSafe(pretty.Bytes()), nil
}

// unescapeHTMLSafe rewrites the six-byte sequences <, >, &
// back into their single-character forms <, >, &. These escapes only
// appear inside JSON string literals (no backslashes outside strings),
// so substitution is byte-safe and JSON remains valid.
func unescapeHTMLSafe(b []byte) []byte {
	b = bytes.ReplaceAll(b, []byte{'\\', 'u', '0', '0', '3', 'c'}, []byte{'<'})
	b = bytes.ReplaceAll(b, []byte{'\\', 'u', '0', '0', '3', 'C'}, []byte{'<'})
	b = bytes.ReplaceAll(b, []byte{'\\', 'u', '0', '0', '3', 'e'}, []byte{'>'})
	b = bytes.ReplaceAll(b, []byte{'\\', 'u', '0', '0', '3', 'E'}, []byte{'>'})
	b = bytes.ReplaceAll(b, []byte{'\\', 'u', '0', '0', '2', '6'}, []byte{'&'})
	return b
}

// xrefFormat reports how the cross-reference table was stored.
func (r *Reader) xrefFormat() string {
	if r.trailer == nil {
		return "unknown"
	}
	if t, ok := r.trailer.Name("Type"); ok && t == "XRef" {
		return "stream"
	}
	if _, ok := r.trailer.Get("XRefStm"); ok {
		return "hybrid"
	}
	return "classical"
}

// orderedKV is one entry of an orderedMap.
type orderedKV struct {
	Key string
	Val any
}

// orderedMap is a JSON object that marshals in insertion order. Used for
// both the top-level dump and every PDF dictionary, so that key order in
// goldens matches the producer's writing order.
type orderedMap []orderedKV

func (m orderedMap) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, kv := range m {
		if i > 0 {
			buf.WriteByte(',')
		}
		kb, err := json.Marshal(kv.Key)
		if err != nil {
			return nil, err
		}
		buf.Write(kb)
		buf.WriteByte(':')
		vb, err := json.Marshal(kv.Val)
		if err != nil {
			return nil, err
		}
		buf.Write(vb)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// dumpValue produces the tagged JSON form of a PDF object.
func dumpValue(v Object, opts DumpOptions) orderedMap {
	switch t := v.(type) {
	case Name:
		return orderedMap{{"name", string(t)}}
	case Integer:
		return orderedMap{{"int", int64(t)}}
	case Real:
		return orderedMap{{"real", float64(t)}}
	case Bool:
		return orderedMap{{"bool", bool(t)}}
	case Null:
		return orderedMap{{"null", nil}}
	case String:
		if looksLikeText(t) {
			return orderedMap{{"text", decodeTextString(t)}}
		}
		return orderedMap{{"hex", hex.EncodeToString(t)}}
	case Reference:
		return orderedMap{{"ref", fmt.Sprintf("%d %d", t.Number, t.Generation)}}
	case Array:
		out := make([]orderedMap, len(t))
		for i, e := range t {
			out[i] = dumpValue(e, opts)
		}
		return orderedMap{{"array", out}}
	case *Dict:
		return orderedMap{{"dict", dumpDictContents(t, opts)}}
	case *Stream:
		return orderedMap{{"stream", dumpStream(t, opts)}}
	case nil:
		return orderedMap{{"null", nil}}
	}
	return orderedMap{{"unknown", fmt.Sprintf("%T", v)}}
}

func dumpDictContents(d *Dict, opts DumpOptions) orderedMap {
	if d == nil {
		return orderedMap{}
	}
	out := orderedMap{}
	for k, v := range d.Iter() {
		out = append(out, orderedKV{k, dumpValue(v, opts)})
	}
	return out
}

func dumpDictTagged(d *Dict, opts DumpOptions) orderedMap {
	return orderedMap{{"dict", dumpDictContents(d, opts)}}
}

func dumpStream(s *Stream, opts DumpOptions) orderedMap {
	out := orderedMap{}
	out = append(out, orderedKV{"dict", dumpDictContents(s.Dict, opts)})
	out = append(out, orderedKV{"raw_length", s.rawLength})

	names, _, ferr := s.reader.streamFilterChain(s.Dict)
	if ferr == nil {
		if names == nil {
			names = []string{}
		}
		out = append(out, orderedKV{"filters", names})
	} else {
		out = append(out, orderedKV{"filters_error", ferr.Error()})
	}

	dec := orderedMap{}
	decoded, derr := s.Content()
	if derr != nil {
		dec = append(dec, orderedKV{"error", derr.Error()})
	} else {
		sum := sha256.Sum256(decoded)
		dec = append(dec, orderedKV{"length", int64(len(decoded))})
		dec = append(dec, orderedKV{"sha256", hex.EncodeToString(sum[:])})
		if opts.PreviewMaxBytes > 0 {
			if p := preview(decoded, opts.PreviewMaxBytes); p != "" {
				dec = append(dec, orderedKV{"preview_utf8", p})
			}
		}
		if opts.InlineStreamContent {
			dec = append(dec, orderedKV{"hex", hex.EncodeToString(decoded)})
		}
	}
	out = append(out, orderedKV{"decoded", dec})
	return out
}

// looksLikeText reports whether s is likely a PDF text string.
//
// The rule:
//
//  1. BOM-prefixed strings (UTF-16BE/LE, UTF-8) → text
//  2. Strings with any C0 control byte (except \t, \r, \n) or DEL → hex
//     This catches file identifiers, hashes and encryption blobs, where
//     random bytes almost always include something in 0x00–0x1F.
//  3. ASCII-only strings → text
//  4. Strings with high bytes (0x80–0xFF) → decode via PDFDocEncoding;
//     if the decoded form contains no U+FFFD (undefined-slot marker)
//     and no control runes, treat as text. This catches PDFDocEncoded
//     content like ActualText with bullets, en-dashes, etc.
func looksLikeText(s String) bool {
	if len(s) >= 2 {
		if s[0] == 0xFE && s[1] == 0xFF {
			return true
		}
		if s[0] == 0xFF && s[1] == 0xFE {
			return true
		}
	}
	if len(s) >= 3 && s[0] == 0xEF && s[1] == 0xBB && s[2] == 0xBF {
		return true
	}
	hasHigh := false
	for _, c := range s {
		if c == '\t' || c == '\r' || c == '\n' {
			continue
		}
		if c < 0x20 || c == 0x7F {
			return false
		}
		if c >= 0x80 {
			hasHigh = true
		}
	}
	if !hasHigh {
		return true
	}
	// Verify the PDFDocEncoded form is clean.
	for _, r := range decodePDFDocEncoding(s) {
		if r == 0xFFFD {
			return false
		}
		if r < 0x20 && r != '\t' && r != '\r' && r != '\n' {
			return false
		}
	}
	return true
}

// preview returns up to max bytes of b as a string, or "" if any byte is
// non-printable. Truncated previews are suffixed with an ellipsis.
func preview(b []byte, max int) string {
	n := len(b)
	truncated := false
	if n > max {
		n = max
		truncated = true
	}
	head := b[:n]
	for _, c := range head {
		if c == '\t' || c == '\r' || c == '\n' {
			continue
		}
		if c < 0x20 || c > 0x7E {
			return ""
		}
	}
	s := string(head)
	if truncated {
		s += "…"
	}
	return s
}
