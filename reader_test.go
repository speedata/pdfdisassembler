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

func TestObjectsIteratorSortedOrder(t *testing.T) {
	r, err := Open(bytes.NewReader(buildMinimalPDF(t)))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer r.Close()
	var nums []int
	for entry := range r.Objects() {
		nums = append(nums, entry.Reference.Number)
	}
	if len(nums) == 0 {
		t.Fatal("no objects iterated")
	}
	for i := 1; i < len(nums); i++ {
		if nums[i-1] >= nums[i] {
			t.Fatalf("Objects() not strictly ascending: %v", nums)
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

func TestResolveHelpers(t *testing.T) {
	data := buildDictPDF(t, []string{
		"<< /Type /Catalog /Pages 2 0 R /IntRef 3 0 R /BoolRef 4 0 R /ArrRef 5 0 R >>",
		"<< /Type /Pages /Kids [] /Count 0 >>",
		"42",
		"true",
		"[ 1 2 3 ]",
	})
	r, err := Open(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer r.Close()
	cat, err := r.Catalog()
	if err != nil {
		t.Fatalf("Catalog: %v", err)
	}
	intRef, _ := cat.Get("IntRef")
	boolRef, _ := cat.Get("BoolRef")
	arrRef, _ := cat.Get("ArrRef")

	if v, err := r.ResolveInt(intRef); err != nil || v != 42 {
		t.Errorf("ResolveInt = %d, %v", v, err)
	}
	if v, err := r.ResolveBool(boolRef); err != nil || !v {
		t.Errorf("ResolveBool = %v, %v", v, err)
	}
	if a, err := r.ResolveArray(arrRef); err != nil || len(a) != 3 {
		t.Errorf("ResolveArray len = %d, %v", len(a), err)
	}
	// Type mismatches must error.
	if _, err := r.ResolveInt(boolRef); err == nil {
		t.Error("ResolveInt on a bool")
	}
	if _, err := r.ResolveBool(arrRef); err == nil {
		t.Error("ResolveBool on an array")
	}
	if _, err := r.ResolveArray(intRef); err == nil {
		t.Error("ResolveArray on an int")
	}

	if r.Trailer() == nil {
		t.Fatal("nil trailer")
	}
	if _, ok := r.Trailer().Get("Root"); !ok {
		t.Error("trailer missing /Root")
	}
}

// A catalog /Version higher than the header version wins (PDF 32000-1 §7.5.5).
func TestVersionCatalogOverride(t *testing.T) {
	data := buildDictPDF(t, []string{
		"<< /Type /Catalog /Pages 2 0 R /Version /2.0 >>",
		"<< /Type /Pages /Kids [] /Count 0 >>",
	})
	r, err := Open(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer r.Close()
	if _, err := r.Catalog(); err != nil { // Version() reads the cached catalog
		t.Fatalf("Catalog: %v", err)
	}
	if v := r.Version(); v != "2.0" {
		t.Errorf("Version = %q, want 2.0 (catalog override)", v)
	}
}

// A non-Reference /Length resolves to itself, so a bare Reader (no xref) drives
// the missing/non-integer/negative guards directly.
func TestStreamLengthRejectsBadLength(t *testing.T) {
	r := &Reader{}
	bad := []struct {
		name string
		set  func(d *Dict)
	}{
		{"missing", func(d *Dict) {}},
		{"non_integer", func(d *Dict) { d.set("Length", String("x")) }},
		{"negative", func(d *Dict) { d.set("Length", Integer(-5)) }},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			d := newDict(nil)
			tc.set(d)
			if _, err := r.streamLength(d); err == nil {
				t.Fatal("expected an error, got nil")
			}
		})
	}

	// Control: a valid non-negative /Length must still resolve.
	t.Run("valid", func(t *testing.T) {
		d := newDict(nil)
		d.set("Length", Integer(42))
		if n, err := r.streamLength(d); err != nil || n != 42 {
			t.Fatalf("streamLength = %d, %v; want 42, nil", n, err)
		}
	})
}

// A bare Reader works because a non-Reference value resolves to itself; each
// helper must error on the wrong type, not pass back a zero value as success.
func TestResolveHelperTypeErrors(t *testing.T) {
	r := &Reader{}
	errCases := []struct {
		name string
		call func() error
	}{
		{"dict_from_int", func() error { _, e := r.ResolveDict(Integer(1)); return e }},
		{"dict_from_null", func() error { _, e := r.ResolveDict(Null{}); return e }},
		{"dict_from_nil", func() error { _, e := r.ResolveDict(nil); return e }},
		{"bool_from_int", func() error { _, e := r.ResolveBool(Integer(1)); return e }},
		{"int_from_bool", func() error { _, e := r.ResolveInt(Bool(true)); return e }},
		{"array_from_int", func() error { _, e := r.ResolveArray(Integer(1)); return e }},
		{"stream_from_int", func() error { _, e := r.DecodeStream(Integer(1)); return e }},
	}
	for _, tc := range errCases {
		if tc.call() == nil {
			t.Errorf("%s: expected an error, got nil", tc.name)
		}
	}
	// Controls: the right type resolves cleanly.
	if b, err := r.ResolveBool(Bool(true)); err != nil || !b {
		t.Errorf("ResolveBool(true) = %v, %v", b, err)
	}
	if n, err := r.ResolveInt(Integer(7)); err != nil || n != 7 {
		t.Errorf("ResolveInt(7) = %v, %v", n, err)
	}
	if a, err := r.ResolveArray(Array{Integer(1)}); err != nil || len(a) != 1 {
		t.Errorf("ResolveArray = %v, %v", a, err)
	}
}

// OpenFile must surface the os.Open error for a missing path, and must not leak
// the descriptor when the file opens but doesn't parse as a PDF.
func TestOpenFileErrors(t *testing.T) {
	if _, err := OpenFile(filepath.Join(t.TempDir(), "missing.pdf")); err == nil {
		t.Error("OpenFile(missing) should error")
	}
	bad := filepath.Join(t.TempDir(), "bad.pdf")
	if err := os.WriteFile(bad, []byte("not a pdf"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenFile(bad); err == nil {
		t.Error("OpenFile(garbage) should error")
	}
}

func TestXrefFormat(t *testing.T) {
	withTrailer := func(set func(d *Dict)) *Reader {
		d := newDict(nil)
		set(d)
		return &Reader{trailer: d}
	}
	if got := (&Reader{}).xrefFormat(); got != "unknown" {
		t.Errorf("nil trailer = %q, want unknown", got)
	}
	if got := withTrailer(func(d *Dict) { d.set("Type", Name("XRef")) }).xrefFormat(); got != "stream" {
		t.Errorf("/Type /XRef = %q, want stream", got)
	}
	if got := withTrailer(func(d *Dict) { d.set("XRefStm", Integer(99)) }).xrefFormat(); got != "hybrid" {
		t.Errorf("/XRefStm = %q, want hybrid", got)
	}
	if got := withTrailer(func(d *Dict) { d.set("Size", Integer(4)) }).xrefFormat(); got != "classical" {
		t.Errorf("plain trailer = %q, want classical", got)
	}
}

// Non-standard /Info keys land in Custom; a non-string entry is skipped, not
// rendered.
func TestDocumentInfoRichFields(t *testing.T) {
	var buf bytes.Buffer
	off := func() int { return buf.Len() }
	fmt.Fprint(&buf, "%PDF-1.7\n%\xE2\xE3\xCF\xD3\n")
	offsets := make([]int, 4)
	offsets[1] = off()
	fmt.Fprint(&buf, "1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n")
	offsets[2] = off()
	fmt.Fprint(&buf, "2 0 obj\n<< /Type /Pages /Count 0 /Kids [] >>\nendobj\n")
	offsets[3] = off()
	fmt.Fprint(&buf, "3 0 obj\n<< /Author (Ada) /Subject (Math) /Keywords (a,b) "+
		"/Creator (X) /Producer (Y) /CreationDate (D:20200102030405Z) "+
		"/ModDate (D:20210102030405Z) /Custom (cval) /NotAString 42 >>\nendobj\n")
	xrefOff := off()
	fmt.Fprint(&buf, "xref\n0 4\n")
	fmt.Fprintf(&buf, "%010d %05d f \n", 0, 65535)
	for i := 1; i <= 3; i++ {
		fmt.Fprintf(&buf, "%010d %05d n \n", offsets[i], 0)
	}
	fmt.Fprint(&buf, "trailer\n<< /Size 4 /Root 1 0 R /Info 3 0 R >>\n")
	fmt.Fprintf(&buf, "startxref\n%d\n%%%%EOF\n", xrefOff)

	r, err := Open(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer r.Close()
	info := r.DocumentInfo()
	if info.Author != "Ada" || info.Subject != "Math" || info.Keywords != "a,b" ||
		info.Creator != "X" || info.Producer != "Y" {
		t.Errorf("string fields wrong: %+v", info)
	}
	if info.CreationDate.Year() != 2020 || info.ModDate.Year() != 2021 {
		t.Errorf("dates wrong: created %v, mod %v", info.CreationDate, info.ModDate)
	}
	if info.Custom["Custom"] != "cval" {
		t.Errorf("Custom[Custom] = %q, want cval", info.Custom["Custom"])
	}
	if _, ok := info.Custom["NotAString"]; ok {
		t.Error("non-string /NotAString should be skipped, not collected")
	}
}

func TestObjectsIteration(t *testing.T) {
	r, err := Open(bytes.NewReader(buildMinimalPDF(t)))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer r.Close()
	var nums []int
	for e := range r.Objects() {
		nums = append(nums, e.Reference.Number)
	}
	if len(nums) < 4 {
		t.Fatalf("iterated %d objects, want >= 4", len(nums))
	}
	for i := 1; i < len(nums); i++ {
		if nums[i] < nums[i-1] {
			t.Errorf("objects out of order: %d before %d", nums[i-1], nums[i])
		}
	}
	count := 0
	for range r.Objects() {
		count++
		break
	}
	if count != 1 {
		t.Fatalf("early break iterated %d, want 1", count)
	}
}

// A reference to an object absent from the xref table resolves to null per
// §7.3.10, not an error.
func TestResolveDanglingReference(t *testing.T) {
	r, err := Open(bytes.NewReader(buildMinimalPDF(t)))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer r.Close()
	v, err := r.Resolve(Reference{Number: 99})
	if err != nil {
		t.Fatalf("dangling ref: %v", err)
	}
	if _, ok := v.(Null); !ok {
		t.Errorf("dangling ref = %T, want Null", v)
	}
}
