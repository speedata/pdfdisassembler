package pdfdisassembler

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"testing"
)

// buildXrefStreamPDF synthesises a PDF that uses an xref stream rather
// than a classical xref table.
func buildXrefStreamPDF(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	off := func() int { return buf.Len() }
	fmt.Fprint(&buf, "%PDF-2.0\n%\xE2\xE3\xCF\xD3\n")

	offsets := make([]int, 4)
	offsets[1] = off()
	fmt.Fprint(&buf, "1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n")
	offsets[2] = off()
	fmt.Fprint(&buf, "2 0 obj\n<< /Type /Pages /Count 0 /Kids [] >>\nendobj\n")

	// Build the xref stream data: 3 entries (objects 0,1,2).
	// Each entry: [type(1), offset(3), gen(1)] big-endian.
	rowSize := 5
	rows := []byte{}
	add := func(typ, f1, f2 uint64) {
		rows = append(rows, byte(typ))
		rows = append(rows, byte(f1>>16), byte(f1>>8), byte(f1))
		rows = append(rows, byte(f2))
	}
	add(0, 0, 0xFFFF) // free
	add(1, uint64(offsets[1]), 0)
	add(1, uint64(offsets[2]), 0)
	_ = rowSize

	var zbuf bytes.Buffer
	zw := zlib.NewWriter(&zbuf)
	zw.Write(rows)
	zw.Close()
	compressed := zbuf.Bytes()

	xrefOff := off()
	fmt.Fprintf(&buf,
		"3 0 obj\n<< /Type /XRef /Size 3 /W [1 3 1] /Root 1 0 R /Filter /FlateDecode /Length %d >>\nstream\n",
		len(compressed))
	buf.Write(compressed)
	fmt.Fprint(&buf, "\nendstream\nendobj\n")
	fmt.Fprintf(&buf, "startxref\n%d\n%%%%EOF\n", xrefOff)
	return buf.Bytes()
}

func TestXrefStream(t *testing.T) {
	data := buildXrefStreamPDF(t)
	r, err := Open(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer r.Close()
	cat, err := r.Catalog()
	if err != nil {
		t.Fatalf("Catalog: %v", err)
	}
	if n, ok := cat.Name("Type"); !ok || n != "Catalog" {
		t.Fatalf("/Type %q ok=%v", n, ok)
	}
	if r.Version() != "2.0" {
		t.Fatalf("version %q", r.Version())
	}
}

func TestXrefRecovery(t *testing.T) {
	// Build a PDF, then point startxref to garbage.
	data := buildMinimalPDF(t)
	// Replace startxref offset to an invalid number.
	idx := bytes.Index(data, []byte("startxref"))
	if idx < 0 {
		t.Fatal("startxref not found in test data")
	}
	// Overwrite the offset following the "startxref\n" with 999999.
	off := idx + len("startxref\n")
	for i := off; i < len(data); i++ {
		if data[i] == '\n' {
			// Overwrite the digits between off and i with 999999.
			width := i - off
			junk := "9999999"[:width]
			copy(data[off:i], junk)
			break
		}
	}
	r, err := Open(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Open after recovery: %v", err)
	}
	defer r.Close()
	cat, err := r.Catalog()
	if err != nil {
		t.Fatalf("Catalog after recovery: %v", err)
	}
	if n, ok := cat.Name("Type"); !ok || n != "Catalog" {
		t.Fatalf("/Type %q ok=%v", n, ok)
	}
}
