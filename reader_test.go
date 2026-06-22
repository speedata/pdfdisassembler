package pdfdisassembler

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"os"
	"path/filepath"
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

// buildPDFWithStream embeds payload as a FlateDecode stream (obj 3) so
// DecodeStream can be exercised.
func buildPDFWithStream(t *testing.T, payload []byte) []byte {
	t.Helper()
	var zbuf bytes.Buffer
	zw := zlib.NewWriter(&zbuf)
	zw.Write(payload)
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
	data := buildPDFWithStream(t, []byte("Hello, stream!"))
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

func TestStreamSizeLimitEnforced(t *testing.T) {
	// obj 3 decompresses to 2 MiB; the 64 KiB cap must reject it.
	data := buildPDFWithStream(t, make([]byte, 2<<20))
	r, err := Open(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer r.Close()

	r.MaxStreamSize = 64 << 10
	if _, err := r.DecodeStream(Reference{Number: 3, Generation: 0}); err == nil {
		t.Fatal("expected error decoding stream larger than MaxStreamSize, got nil")
	}
}

func TestWithMaxStreamSizeOption(t *testing.T) {
	data := buildPDFWithStream(t, make([]byte, 2<<20))
	r, err := Open(bytes.NewReader(data), WithMaxStreamSize(64<<10))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer r.Close()
	if r.MaxStreamSize != 64<<10 {
		t.Fatalf("MaxStreamSize = %d, want %d", r.MaxStreamSize, 64<<10)
	}
	if _, err := r.DecodeStream(Reference{Number: 3, Generation: 0}); err == nil {
		t.Fatal("expected error: stream exceeds the option-set cap")
	}
}

func TestWithMaxStreamSizeDisable(t *testing.T) {
	data := buildPDFWithStream(t, make([]byte, 2<<20))
	r, err := Open(bytes.NewReader(data), WithMaxStreamSize(0))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer r.Close()
	out, err := r.DecodeStream(Reference{Number: 3, Generation: 0})
	if err != nil {
		t.Fatalf("DecodeStream with cap disabled: %v", err)
	}
	if len(out) != 2<<20 {
		t.Fatalf("decoded %d bytes, want %d", len(out), 2<<20)
	}
}

func TestDefaultMaxStreamSizeSet(t *testing.T) {
	// Open must install a finite default so Open-time decodes are bounded.
	data := buildMinimalPDF(t)
	r, err := Open(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer r.Close()
	if r.MaxStreamSize != DefaultMaxStreamSize {
		t.Fatalf("MaxStreamSize = %d, want default %d", r.MaxStreamSize, DefaultMaxStreamSize)
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

// buildDictPDF puts each body in objs as object i+1 of a classical-xref PDF
// (obj 1 is the catalog). Bodies are plain objects (no streams).
func buildDictPDF(t *testing.T, objs []string) []byte {
	t.Helper()
	var buf bytes.Buffer
	fmt.Fprint(&buf, "%PDF-1.7\n%\xE2\xE3\xCF\xD3\n")
	offsets := make([]int, len(objs)+1)
	for i, body := range objs {
		offsets[i+1] = buf.Len()
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", i+1, body)
	}
	xrefOff := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 %d\n%010d %05d f \n", len(objs)+1, 0, 65535)
	for i := 1; i <= len(objs); i++ {
		fmt.Fprintf(&buf, "%010d %05d n \n", offsets[i], 0)
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n",
		len(objs)+1, xrefOff)
	return buf.Bytes()
}

func TestEmbeddedFiles(t *testing.T) {
	data := buildDictPDF(t, []string{
		"<< /Type /Catalog /Pages 2 0 R /Names << /EmbeddedFiles 3 0 R >> >>",
		"<< /Type /Pages /Kids [] /Count 0 >>",
		"<< /Names [ (a.xml) 4 0 R (b.xml) 5 0 R ] >>",
		"<< /Type /Filespec /F (a.xml) >>",
		"<< /Type /Filespec /F (b.xml) >>",
	})
	r, err := Open(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer r.Close()
	ef := r.EmbeddedFiles()
	if len(ef) != 2 {
		t.Fatalf("got %d files, want 2", len(ef))
	}
	if ef[0].Name != "a.xml" || ef[1].Name != "b.xml" {
		t.Fatalf("names %q, %q", ef[0].Name, ef[1].Name)
	}
	if f, ok := ef[0].Spec.String("F"); !ok || f != "a.xml" {
		t.Fatalf("spec /F %q ok=%v", f, ok)
	}
}

func TestEmbeddedFilesNestedKids(t *testing.T) {
	data := buildDictPDF(t, []string{
		"<< /Type /Catalog /Pages 2 0 R /Names << /EmbeddedFiles 3 0 R >> >>",
		"<< /Type /Pages /Kids [] /Count 0 >>",
		"<< /Kids [ 4 0 R ] >>",
		"<< /Names [ (a.xml) 5 0 R ] >>",
		"<< /Type /Filespec /F (a.xml) >>",
	})
	r, err := Open(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer r.Close()
	if ef := r.EmbeddedFiles(); len(ef) != 1 || ef[0].Name != "a.xml" {
		t.Fatalf("got %+v, want one a.xml", ef)
	}
}

func TestEmbeddedFilesCyclicKidsTerminates(t *testing.T) {
	// obj 3's /Kids references itself; the walk must terminate, not overflow.
	data := buildDictPDF(t, []string{
		"<< /Type /Catalog /Pages 2 0 R /Names << /EmbeddedFiles 3 0 R >> >>",
		"<< /Type /Pages /Kids [] /Count 0 >>",
		"<< /Kids [ 3 0 R ] >>",
	})
	r, err := Open(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer r.Close()
	if ef := r.EmbeddedFiles(); len(ef) != 0 {
		t.Fatalf("got %d files, want 0", len(ef))
	}
}

func TestEmbeddedFilesNone(t *testing.T) {
	data := buildDictPDF(t, []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [] /Count 0 >>",
	})
	r, err := Open(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer r.Close()
	if ef := r.EmbeddedFiles(); ef != nil {
		t.Fatalf("got %+v, want nil", ef)
	}
}

// FuzzOpen asserts the read pipeline never panics on arbitrary input: Open and
// every accessor may return an error, but must not crash the process.
func FuzzOpen(f *testing.F) {
	seeds, _ := filepath.Glob("testdata/fixtures/*/input.pdf")
	for _, p := range seeds {
		if b, err := os.ReadFile(p); err == nil {
			f.Add(b)
		}
	}
	f.Add([]byte("%PDF-1.7\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		r, err := Open(bytes.NewReader(data))
		if err != nil {
			return
		}
		defer r.Close()
		_, _ = r.Catalog()
		_ = r.DocumentInfo()
		_ = r.EmbeddedFiles()
		_ = r.Version()
		for entry := range r.Objects() {
			if s, ok := entry.Object.(*Stream); ok {
				_, _ = s.Content()
			}
		}
	})
}
