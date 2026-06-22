package pdfdisassembler

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"runtime"
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

// buildObjStmHugeN builds a PDF reachable via an xref stream where object 4
// is a type-2 entry inside ObjStm 3. The ObjStm decodes to contentLen zero
// bytes (cheap: FlateDecode compresses them to a few KB) but declares
// /N == contentLen. A reader that trusts /N as a slice capacity balloons the
// few-KB file into contentLen*sizeof(pair) bytes before parsing a single entry.
func buildObjStmHugeN(t *testing.T, contentLen int) []byte {
	t.Helper()
	var zbuf bytes.Buffer
	zw := zlib.NewWriter(&zbuf)
	if _, err := zw.Write(make([]byte, contentLen)); err != nil {
		t.Fatal(err)
	}
	zw.Close()
	compressed := zbuf.Bytes()

	var buf bytes.Buffer
	buf.WriteString("%PDF-1.5\n%\xe2\xe3\xcf\xd3\n")

	off3 := buf.Len()
	fmt.Fprintf(&buf, "3 0 obj\n<< /Type /ObjStm /N %d /First 4 /Filter /FlateDecode /Length %d >>\nstream\n",
		contentLen, len(compressed))
	buf.Write(compressed)
	buf.WriteString("\nendstream\nendobj\n")

	off2 := buf.Len()
	rows := []byte{
		0x01, byte(off3 >> 8), byte(off3), 0x00, // obj 3: type 1 @ off3
		0x02, 0x00, 0x03, 0x00, // obj 4: type 2 -> ObjStm 3, index 0
	}
	fmt.Fprintf(&buf, "2 0 obj\n<< /Type /XRef /W [ 1 2 1 ] /Index [ 3 2 ] /Size 5 /Root 1 0 R /Length %d >>\nstream\n",
		len(rows))
	buf.Write(rows)
	buf.WriteString("\nendstream\nendobj\n")

	fmt.Fprintf(&buf, "startxref\n%d\n%%%%EOF\n", off2)
	return buf.Bytes()
}

func TestObjStmHugeNDoesNotAmplifyAllocation(t *testing.T) {
	const contentLen = 16 << 20 // == DefaultMaxStreamSize, the largest /N can be
	data := buildObjStmHugeN(t, contentLen)
	r, err := Open(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer r.Close()

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	_, err = r.Resolve(Reference{Number: 4, Generation: 0})
	runtime.ReadMemStats(&after)
	if err == nil {
		t.Fatal("expected an error resolving the hostile ObjStm entry, got nil")
	}
	// Decoding contentLen zero bytes legitimately costs ~2*contentLen; the
	// 256 MiB the /N prealloc would add is well past this ceiling.
	const limit = 128 << 20
	if used := after.TotalAlloc - before.TotalAlloc; used > limit {
		t.Fatalf("resolving a %d-byte ObjStm allocated %d bytes (> %d limit); /N is amplifying allocation",
			contentLen, used, limit)
	}
}

// Capping the prealloc must not truncate a legitimate ObjStm whose entry count
// exceeds the cap: object 100+i carries the value 100+i, and resolving one past
// the cap must still return its exact value (proving append grew the slice).
func TestObjStmManyObjectsResolvePastPrealloc(t *testing.T) {
	const m = 5000 // > the internal maxObjStmPrealloc (4096)

	var body bytes.Buffer
	offsets := make([]int, m)
	for i := 0; i < m; i++ {
		offsets[i] = body.Len()
		fmt.Fprintf(&body, "%d ", 100+i)
	}
	var head bytes.Buffer
	for i := 0; i < m; i++ {
		fmt.Fprintf(&head, "%d %d ", 100+i, offsets[i])
	}
	content := head.String() + body.String()

	var buf bytes.Buffer
	buf.WriteString("%PDF-1.5\n%\xe2\xe3\xcf\xd3\n")
	off1 := buf.Len()
	fmt.Fprintf(&buf, "1 0 obj\n<< /Type /ObjStm /N %d /First %d /Length %d >>\nstream\n",
		m, head.Len(), len(content))
	buf.WriteString(content)
	buf.WriteString("\nendstream\nendobj\n")

	last := 100 + m - 1
	off2 := buf.Len()
	rows := []byte{
		0x01, byte(off1 >> 8), byte(off1), 0x00, 0x00, // obj 1: type 1 @ off1
		0x02, 0x00, 0x01, byte((m - 1) >> 8), byte((m - 1) & 0xff), // last obj: type 2 -> ObjStm 1, idx m-1
	}
	fmt.Fprintf(&buf, "2 0 obj\n<< /Type /XRef /W [ 1 2 2 ] /Index [ 1 1 %d 1 ] /Size %d /Root 1 0 R /Length %d >>\nstream\n",
		last, last+1, len(rows))
	buf.Write(rows)
	buf.WriteString("\nendstream\nendobj\n")
	fmt.Fprintf(&buf, "startxref\n%d\n%%%%EOF\n", off2)

	r, err := Open(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer r.Close()
	v, err := r.Resolve(Reference{Number: last, Generation: 0})
	if err != nil {
		t.Fatalf("Resolve object %d: %v", last, err)
	}
	n, ok := v.(Integer)
	if !ok || int(n) != last {
		t.Fatalf("object %d resolved to %v (%T), want Integer %d", last, v, v, last)
	}
}

// With no startxref and no "trailer" keyword, recovery must scan the rebuilt
// objects for a /Type /Catalog and synthesise a trailer pointing at it.
func TestRecoverXrefViaCatalogScan(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteString("%PDF-1.7\n%\xe2\xe3\xcf\xd3\n")
	buf.WriteString("1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n")
	buf.WriteString("2 0 obj\n<< /Type /Pages /Count 0 /Kids [] >>\nendobj\n")
	buf.WriteString("%%EOF\n")

	r, err := Open(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer r.Close()
	cat, err := r.Catalog()
	if err != nil {
		t.Fatalf("Catalog: %v", err)
	}
	if n, ok := cat.Name("Type"); !ok || n != "Catalog" {
		t.Fatalf("/Type %q ok=%v, want Catalog", n, ok)
	}
}

// An incremental update appends a second xref section whose /Prev points back
// at the first. The newer section must win: object 1 resolves to its updated
// body, and trailer keys present only in the older section still resolve.
func TestPrevChainNewestSectionWins(t *testing.T) {
	var buf bytes.Buffer
	w := func(s string) int { off := buf.Len(); buf.WriteString(s); return off }
	w("%PDF-1.7\n%\xe2\xe3\xcf\xd3\n")
	off1v1 := w("1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n")
	off2 := w("2 0 obj\n<< /Type /Pages /Count 0 /Kids [] >>\nendobj\n")

	xref1 := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 3\n%010d %05d f \n%010d %05d n \n%010d %05d n \n",
		0, 65535, off1v1, 0, off2, 0)
	buf.WriteString("trailer\n<< /Size 3 /Root 1 0 R /Info 2 0 R >>\n")
	fmt.Fprintf(&buf, "startxref\n%d\n%%%%EOF\n", xref1)

	off1v2 := buf.Len()
	buf.WriteString("1 0 obj\n<< /Type /Catalog /Pages 2 0 R /Lang (en-US) >>\nendobj\n")
	xref2 := buf.Len()
	fmt.Fprintf(&buf, "xref\n1 1\n%010d %05d n \n", off1v2, 0)
	fmt.Fprintf(&buf, "trailer\n<< /Size 3 /Root 1 0 R /Prev %d >>\n", xref1)
	fmt.Fprintf(&buf, "startxref\n%d\n%%%%EOF\n", xref2)

	r, err := Open(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer r.Close()

	cat, err := r.Catalog()
	if err != nil {
		t.Fatalf("Catalog: %v", err)
	}
	if lang, ok := cat.String("Lang"); !ok || lang != "en-US" {
		t.Errorf("/Lang = %q ok=%v, want en-US (updated object 1 not used)", lang, ok)
	}
	// /Info lives only in the older trailer; the merge must preserve it.
	if _, ok := r.Trailer().Get("Info"); !ok {
		t.Error("older trailer's /Info lost after /Prev merge")
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
