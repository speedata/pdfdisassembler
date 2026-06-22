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

// buildXrefStreamPDFWithW builds a PDF whose cross-reference stream (obj 3)
// declares the given /W array and carries content as its (unfiltered) row data.
func buildXrefStreamPDFWithW(t *testing.T, wArray, content string) []byte {
	t.Helper()
	var buf bytes.Buffer
	off := map[int]int{}
	w := func(n int, body string) {
		off[n] = buf.Len()
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", n, body)
	}
	buf.WriteString("%PDF-1.5\n%\xe2\xe3\xcf\xd3\n")
	w(1, "<< /Type /Catalog /Pages 2 0 R >>")
	w(2, "<< /Type /Pages /Kids [] /Count 0 >>")
	off[3] = buf.Len()
	fmt.Fprintf(&buf, "3 0 obj\n<< /Type /XRef /W %s /Size 1 /Root 1 0 R /Length %d >>\nstream\n",
		wArray, len(content))
	buf.WriteString(content)
	buf.WriteString("\nendstream\nendobj\n")
	fmt.Fprintf(&buf, "startxref\n%d\n%%%%EOF\n", off[3])
	return buf.Bytes()
}

// buildObjStmPDF builds a PDF whose catalog (obj 1) lives inside an object
// stream (obj 3), reached via a type-2 entry in the xref stream (obj 2). The
// ObjStm dict declares declaredN / declaredFirst, which the caller can set to
// hostile values; the actual stream is always "1 0 " + catalogBody.
func buildObjStmPDF(t *testing.T, declaredN, declaredFirst int64, catalogBody string) []byte {
	t.Helper()
	objstm := "1 0 " + catalogBody // header "1 0 " (4 bytes), catalog at offset 4

	var buf bytes.Buffer
	buf.WriteString("%PDF-1.5\n%\xe2\xe3\xcf\xd3\n")

	off3 := buf.Len()
	fmt.Fprintf(&buf, "3 0 obj\n<< /Type /ObjStm /N %d /First %d /Length %d >>\nstream\n%s\nendstream\nendobj\n",
		declaredN, declaredFirst, len(objstm), objstm)

	off2 := buf.Len()
	rows := []byte{
		0x00, 0x00, 0x00, 0x00, // obj 0: free
		0x02, 0x00, 0x03, 0x00, // obj 1: type 2 -> ObjStm 3, index 0
		0x01, byte(off2 >> 8), byte(off2), 0x00, // obj 2: type 1 @ off2
		0x01, byte(off3 >> 8), byte(off3), 0x00, // obj 3: type 1 @ off3
	}
	fmt.Fprintf(&buf, "2 0 obj\n<< /Type /XRef /W [ 1 2 1 ] /Index [ 0 4 ] /Size 4 /Root 1 0 R /Length %d >>\nstream\n",
		len(rows))
	buf.Write(rows)
	buf.WriteString("\nendstream\nendobj\n")

	fmt.Fprintf(&buf, "startxref\n%d\n%%%%EOF\n", off2)
	return buf.Bytes()
}

func TestXrefStreamNegativeWidthRecovers(t *testing.T) {
	// /W [1 -2 10]: the negative width makes the row decode slice row[1:-1].
	// The parser must recover (not panic), leaving the catalog reachable.
	data := buildXrefStreamPDFWithW(t, "[ 1 -2 10 ]", "123456789")
	r, err := Open(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer r.Close()
	if _, err := r.Catalog(); err != nil {
		t.Fatalf("Catalog after recovery: %v", err)
	}
}

// Control: a well-formed ObjStm catalog must resolve, proving the harness and
// the type-2 path work (so the hostile cases below aren't false positives).
func TestObjStmCatalogBaseline(t *testing.T) {
	data := buildObjStmPDF(t, 1, 4, "<< /Type /Catalog >>")
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
}

func TestObjStmRejectsHostileHeader(t *testing.T) {
	// Absurd attacker-controlled /First and /N must surface as errors, not
	// slice/make panics.
	tests := []struct {
		name          string
		declaredN     int64
		declaredFirst int64
	}{
		{"first beyond stream", 1, 1 << 60},
		{"absurd N", 1 << 60, 4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := buildObjStmPDF(t, tt.declaredN, tt.declaredFirst, "<< /Type /Catalog >>")
			r, err := Open(bytes.NewReader(data))
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			defer r.Close()
			if _, err := r.Catalog(); err == nil {
				t.Fatal("expected an error, got nil")
			}
		})
	}
}

// A classical xref subsection declaring far more entries than the file holds
// must be handled gracefully (recover), not over-read the buffer. The bound is
// also overflow-safe on 32-bit by construction (untestable on a 64-bit run).
func TestClassicalXrefHugeCountRecovers(t *testing.T) {
	data := buildMinimalPDF(t)
	data = bytes.Replace(data, []byte("xref\n0 5\n"), []byte("xref\n0 999999999\n"), 1)
	r, err := Open(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer r.Close()
	if _, err := r.Catalog(); err != nil {
		t.Fatalf("Catalog: %v", err)
	}
}
