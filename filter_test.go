package pdfdisassembler

import (
	"bytes"
	"compress/lzw"
	"compress/zlib"
	"encoding/ascii85"
	"fmt"
	"testing"
)

// buildStreamObjectPDF wraps stream as indirect object 3 with the given stream
// dict entries (e.g. "/Filter /LZWDecode ..."), reachable via a classical xref.
func buildStreamObjectPDF(t *testing.T, dictEntries string, stream []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	off := func() int { return buf.Len() }
	buf.WriteString("%PDF-1.7\n%\xe2\xe3\xcf\xd3\n")
	offsets := make([]int, 4)

	offsets[1] = off()
	buf.WriteString("1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n")
	offsets[2] = off()
	buf.WriteString("2 0 obj\n<< /Type /Pages /Count 0 /Kids [] >>\nendobj\n")
	offsets[3] = off()
	fmt.Fprintf(&buf, "3 0 obj\n<< %s /Length %d >>\nstream\n", dictEntries, len(stream))
	buf.Write(stream)
	buf.WriteString("\nendstream\nendobj\n")

	xrefOff := off()
	buf.WriteString("xref\n0 4\n")
	fmt.Fprintf(&buf, "%010d %05d f \n", 0, 65535)
	for i := 1; i <= 3; i++ {
		fmt.Fprintf(&buf, "%010d %05d n \n", offsets[i], 0)
	}
	buf.WriteString("trailer\n<< /Size 4 /Root 1 0 R >>\n")
	fmt.Fprintf(&buf, "startxref\n%d\n%%%%EOF\n", xrefOff)
	return buf.Bytes()
}

func streamObject3(t *testing.T, data []byte) []byte {
	t.Helper()
	r, err := Open(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { r.Close() })
	v, err := r.Resolve(Reference{Number: 3, Generation: 0})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	stm, ok := v.(*Stream)
	if !ok {
		t.Fatalf("object 3 is %T, want *Stream", v)
	}
	got, err := stm.Content()
	if err != nil {
		t.Fatalf("Content: %v", err)
	}
	return got
}

// A stream declaring /DecodeParms << /EarlyChange 0 >> must decode with early
// change off. The stdlib LZW writer emits the non-early convention, so honouring
// the parameter reproduces the input; ignoring it garbles a stream past the
// first code-width boundary.
func TestLZWStreamEarlyChangeZero(t *testing.T) {
	orig := make([]byte, 4096)
	x := uint32(99)
	for i := range orig {
		x = x*1664525 + 1013904223
		orig[i] = byte(x >> 24)
	}
	var enc bytes.Buffer
	w := lzw.NewWriter(&enc, lzw.MSB, 8)
	w.Write(orig)
	w.Close()

	got := streamObject3(t, buildStreamObjectPDF(t,
		"/Filter /LZWDecode /DecodeParms << /EarlyChange 0 >>", enc.Bytes()))
	if !bytes.Equal(got, orig) {
		t.Fatal("LZW stream with /EarlyChange 0 decoded incorrectly")
	}
}

// A /Filter array applies filters in order: the raw bytes are ASCII85 wrapping a
// FlateDecode stream, so the chain must un-ASCII85 then inflate.
func TestStreamFilterChainArray(t *testing.T) {
	orig := []byte("chained filters: ASCII85 over Flate over the original bytes")
	var fl bytes.Buffer
	zw := zlib.NewWriter(&fl)
	zw.Write(orig)
	zw.Close()
	a85 := make([]byte, ascii85.MaxEncodedLen(fl.Len()))
	n := ascii85.Encode(a85, fl.Bytes())
	stream := append(a85[:n:n], '~', '>')

	got := streamObject3(t, buildStreamObjectPDF(t,
		"/Filter [ /ASCII85Decode /FlateDecode ]", stream))
	if !bytes.Equal(got, orig) {
		t.Fatalf("chained decode = %q, want %q", got, orig)
	}
}
