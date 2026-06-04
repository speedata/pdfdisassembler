package pdfdisassembler

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"strings"
	"testing"
)

// buildMinimalPDF constructs a tiny valid PDF in memory: a catalog, a
// pages tree with one empty page, an Info dict, and a classical xref. It
// returns the raw bytes.
func buildMinimalPDF(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	off := func() int { return buf.Len() }
	fmt.Fprint(&buf, "%PDF-1.7\n%\xE2\xE3\xCF\xD3\n")

	offsets := make([]int, 5) // index 1..4

	offsets[1] = off()
	fmt.Fprint(&buf, "1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n")

	offsets[2] = off()
	fmt.Fprint(&buf, "2 0 obj\n<< /Type /Pages /Count 1 /Kids [ 3 0 R ] >>\nendobj\n")

	offsets[3] = off()
	fmt.Fprint(&buf, "3 0 obj\n<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] >>\nendobj\n")

	offsets[4] = off()
	fmt.Fprint(&buf, "4 0 obj\n<< /Title (Hello) /Producer (pdfdisassembler-test) >>\nendobj\n")

	xrefOff := off()
	fmt.Fprint(&buf, "xref\n0 5\n")
	fmt.Fprintf(&buf, "%010d %05d f \n", 0, 65535)
	for i := 1; i <= 4; i++ {
		fmt.Fprintf(&buf, "%010d %05d n \n", offsets[i], 0)
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size 5 /Root 1 0 R /Info 4 0 R >>\n")
	fmt.Fprintf(&buf, "startxref\n%d\n%%%%EOF\n", xrefOff)
	return buf.Bytes()
}

func TestOpenMinimal(t *testing.T) {
	data := buildMinimalPDF(t)
	r, err := Open(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer r.Close()

	if r.Version() != "1.7" {
		t.Fatalf("version %q", r.Version())
	}
	cat, err := r.Catalog()
	if err != nil {
		t.Fatalf("Catalog: %v", err)
	}
	n, ok := cat.Name("Type")
	if !ok || n != "Catalog" {
		t.Fatalf("/Type %q ok=%v", n, ok)
	}
	pages, ok := cat.Dict("Pages")
	if !ok {
		t.Fatal("Catalog.Dict(Pages)")
	}
	count, ok := pages.Int("Count")
	if !ok || count != 1 {
		t.Fatalf("/Count %d ok=%v", count, ok)
	}
}

func TestDocumentInfo(t *testing.T) {
	data := buildMinimalPDF(t)
	r, _ := Open(bytes.NewReader(data))
	defer r.Close()
	info := r.DocumentInfo()
	if info.Title != "Hello" {
		t.Fatalf("Title %q", info.Title)
	}
	if !strings.HasPrefix(info.Producer, "pdfdisassembler") {
		t.Fatalf("Producer %q", info.Producer)
	}
}

func TestObjectsIterator(t *testing.T) {
	data := buildMinimalPDF(t)
	r, _ := Open(bytes.NewReader(data))
	defer r.Close()
	count := 0
	seen := map[int]bool{}
	for entry := range r.Objects() {
		count++
		seen[entry.Reference.Number] = true
	}
	if count != 4 {
		t.Fatalf("count %d", count)
	}
	for i := 1; i <= 4; i++ {
		if !seen[i] {
			t.Fatalf("missing object %d", i)
		}
	}
}

// buildPDFWithStream embeds a FlateDecode stream so DecodeStream can be
// exercised.
func buildPDFWithStream(t *testing.T) []byte {
	t.Helper()
	const payload = "Hello, stream!"
	var zbuf bytes.Buffer
	zw := zlib.NewWriter(&zbuf)
	zw.Write([]byte(payload))
	zw.Close()
	zdata := zbuf.Bytes()

	var buf bytes.Buffer
	off := func() int { return buf.Len() }
	fmt.Fprint(&buf, "%PDF-1.7\n%\xE2\xE3\xCF\xD3\n")

	offsets := make([]int, 4)

	offsets[1] = off()
	fmt.Fprint(&buf, "1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n")

	offsets[2] = off()
	fmt.Fprint(&buf, "2 0 obj\n<< /Type /Pages /Count 0 /Kids [] >>\nendobj\n")

	offsets[3] = off()
	fmt.Fprintf(&buf, "3 0 obj\n<< /Length %d /Filter /FlateDecode >>\nstream\n", len(zdata))
	buf.Write(zdata)
	fmt.Fprint(&buf, "\nendstream\nendobj\n")

	xrefOff := off()
	fmt.Fprint(&buf, "xref\n0 4\n")
	fmt.Fprintf(&buf, "%010d %05d f \n", 0, 65535)
	for i := 1; i <= 3; i++ {
		fmt.Fprintf(&buf, "%010d %05d n \n", offsets[i], 0)
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size 4 /Root 1 0 R >>\n")
	fmt.Fprintf(&buf, "startxref\n%d\n%%%%EOF\n", xrefOff)
	return buf.Bytes()
}

func TestDecodeStream(t *testing.T) {
	data := buildPDFWithStream(t)
	r, err := Open(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer r.Close()

	content, err := r.DecodeStream(Reference{Number: 3, Generation: 0})
	if err != nil {
		t.Fatalf("DecodeStream: %v", err)
	}
	if string(content) != "Hello, stream!" {
		t.Fatalf("content %q", content)
	}
}

func TestTextDecodingUTF16BE(t *testing.T) {
	// Hello in UTF-16BE with BOM.
	b := String{0xFE, 0xFF, 0, 'H', 0, 'i'}
	got := decodeTextString(b)
	if got != "Hi" {
		t.Fatalf("got %q", got)
	}
}

func TestTextDecodingPDFDocEncoding(t *testing.T) {
	b := String("Caf\xe9")
	got := decodeTextString(b)
	if got != "Café" {
		t.Fatalf("got %q", got)
	}
}

func TestParseDate(t *testing.T) {
	d := parseDate("D:20260604120000+02'00'")
	if d.IsZero() {
		t.Fatal("zero")
	}
	if d.Year() != 2026 || d.Month() != 6 || d.Day() != 4 {
		t.Fatalf("date %v", d)
	}
	if d.Hour() != 12 {
		t.Fatalf("hour %d", d.Hour())
	}
}
