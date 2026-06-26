package pdfdisassembler

import (
	"bytes"
	"path/filepath"
	"testing"
)

func TestPageInheritanceFixture(t *testing.T) {
	r, err := OpenFile(filepath.Join("testdata", "fixtures", "page-inheritance", "input.pdf"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer r.Close()

	n, err := r.PageCount()
	if err != nil {
		t.Fatalf("PageCount: %v", err)
	}
	if n != 2 {
		t.Fatalf("PageCount = %d, want 2", n)
	}

	// Page 0 inherits everything: MediaBox/Rotate/Resources two levels up
	// (the /Pages root), CropBox one level up.
	p0, err := r.Page(0)
	if err != nil {
		t.Fatalf("Page(0): %v", err)
	}
	if box, ok := p0.Box(MediaBox); !ok || box != (Rect{0, 0, 612, 792}) {
		t.Errorf("page 0 MediaBox = %+v ok=%v, want {0 0 612 792}", box, ok)
	}
	if box, ok := p0.Box(CropBox); !ok || box != (Rect{10, 10, 602, 782}) {
		t.Errorf("page 0 CropBox = %+v ok=%v, want {10 10 602 782}", box, ok)
	}
	if rot := p0.Rotation(); rot != 90 {
		t.Errorf("page 0 Rotation = %d, want 90", rot)
	}
	res, ok := p0.Resources()
	if !ok {
		t.Fatalf("page 0 Resources not found")
	}
	if _, ok := res.Dict("Font"); !ok {
		t.Errorf("page 0 Resources missing /Font: keys %v", res.Keys())
	}
	// Boxes that no ancestor defines are absent (no spec-default substitution).
	if box, ok := p0.Box(BleedBox); ok {
		t.Errorf("page 0 BleedBox = %+v, want absent", box)
	}

	// Page 1 overrides MediaBox and Rotate locally, still inherits CropBox.
	p1, err := r.Page(1)
	if err != nil {
		t.Fatalf("Page(1): %v", err)
	}
	if box, ok := p1.Box(MediaBox); !ok || box != (Rect{0, 0, 200, 200}) {
		t.Errorf("page 1 MediaBox = %+v ok=%v, want {0 0 200 200}", box, ok)
	}
	if box, ok := p1.Box(CropBox); !ok || box != (Rect{10, 10, 602, 782}) {
		t.Errorf("page 1 CropBox = %+v ok=%v, want inherited {10 10 602 782}", box, ok)
	}
	if rot := p1.Rotation(); rot != 0 {
		t.Errorf("page 1 Rotation = %d, want 0 (override)", rot)
	}
}

func TestPageContentsArrayFixture(t *testing.T) {
	r, err := OpenFile(filepath.Join("testdata", "fixtures", "page-contents-array", "input.pdf"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer r.Close()

	p, err := r.Page(0)
	if err != nil {
		t.Fatalf("Page(0): %v", err)
	}
	streams, err := p.ContentStreams()
	if err != nil {
		t.Fatalf("ContentStreams: %v", err)
	}
	if len(streams) != 2 {
		t.Fatalf("got %d content streams, want 2", len(streams))
	}
	if raw, err := streams[0].RawBytes(); err != nil || string(raw) != "q 1 0 0 1 50 50 cm" {
		t.Errorf("stream 0 RawBytes = %q err=%v", raw, err)
	}

	content, err := p.Content()
	if err != nil {
		t.Fatalf("Content: %v", err)
	}
	want := "q 1 0 0 1 50 50 cm\nBT /F1 12 Tf (Hello) Tj ET"
	if string(content) != want {
		t.Errorf("Content = %q, want %q", content, want)
	}
}

func TestPageContentSingleStream(t *testing.T) {
	data := buildDictPDF(t, []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Count 1 /Kids [ 3 0 R ] >>",
		"<< /Type /Page /Parent 2 0 R /Contents 4 0 R >>",
		"<< /Length 5 >>\nstream\nhello\nendstream",
	})
	r, err := Open(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer r.Close()
	p, err := r.Page(0)
	if err != nil {
		t.Fatalf("Page(0): %v", err)
	}
	if got, err := p.Content(); err != nil || string(got) != "hello" {
		t.Errorf("Content = %q err=%v, want \"hello\"", got, err)
	}
}

func TestPageContentNone(t *testing.T) {
	data := buildDictPDF(t, []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Count 1 /Kids [ 3 0 R ] >>",
		"<< /Type /Page /Parent 2 0 R >>",
	})
	r, err := Open(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer r.Close()
	p, _ := r.Page(0)
	streams, err := p.ContentStreams()
	if err != nil || streams != nil {
		t.Errorf("ContentStreams = %v err=%v, want nil", streams, err)
	}
	if got, err := p.Content(); err != nil || len(got) != 0 {
		t.Errorf("Content = %q err=%v, want empty", got, err)
	}
}

func TestPageRotationNormalization(t *testing.T) {
	cases := []struct {
		rotate string
		want   int
	}{
		{"450", 90},
		{"-90", 270},
		{"360", 0},
		{"270", 270},
		{"45", 0},  // not a multiple of 90 → defensive 0
		{"(x)", 0}, // wrong type → 0
	}
	for _, tc := range cases {
		data := buildDictPDF(t, []string{
			"<< /Type /Catalog /Pages 2 0 R >>",
			"<< /Type /Pages /Count 1 /Kids [ 3 0 R ] >>",
			"<< /Type /Page /Parent 2 0 R /Rotate " + tc.rotate + " >>",
		})
		r, err := Open(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("Open(%s): %v", tc.rotate, err)
		}
		p, _ := r.Page(0)
		if got := p.Rotation(); got != tc.want {
			t.Errorf("Rotate %s → %d, want %d", tc.rotate, got, tc.want)
		}
		r.Close()
	}
}

func TestPageBoxNormalization(t *testing.T) {
	// Corners written in the wrong order must come back normalised, and the
	// box may be a Real-valued array.
	data := buildDictPDF(t, []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Count 1 /Kids [ 3 0 R ] >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [612.0 792.0 0 0] >>",
	})
	r, err := Open(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer r.Close()
	p, _ := r.Page(0)
	box, ok := p.Box(MediaBox)
	if !ok || box != (Rect{0, 0, 612, 792}) {
		t.Fatalf("MediaBox = %+v ok=%v, want {0 0 612 792}", box, ok)
	}
	if box.Width() != 612 || box.Height() != 792 {
		t.Errorf("Width/Height = %v/%v, want 612/792", box.Width(), box.Height())
	}
}

func TestPageBoxMalformed(t *testing.T) {
	// A box that is not a 4-number array must report absent, not panic.
	data := buildDictPDF(t, []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Count 1 /Kids [ 3 0 R ] >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612] /CropBox (nope) >>",
	})
	r, err := Open(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer r.Close()
	p, _ := r.Page(0)
	if box, ok := p.Box(MediaBox); ok {
		t.Errorf("short MediaBox = %+v, want absent", box)
	}
	if box, ok := p.Box(CropBox); ok {
		t.Errorf("string CropBox = %+v, want absent", box)
	}
	if boxes := p.Boxes(); len(boxes) != 0 {
		t.Errorf("Boxes = %v, want empty", boxes)
	}
}

func TestPageIndexOutOfRange(t *testing.T) {
	data := buildDictPDF(t, []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Count 1 /Kids [ 3 0 R ] >>",
		"<< /Type /Page /Parent 2 0 R >>",
	})
	r, err := Open(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer r.Close()
	if _, err := r.Page(-1); err == nil {
		t.Error("Page(-1) = nil error, want out-of-range error")
	}
	if _, err := r.Page(1); err == nil {
		t.Error("Page(1) = nil error, want out-of-range error")
	}
}

func TestPageCyclicKidsTerminates(t *testing.T) {
	// obj 3's /Kids points back to obj 2; the walk must terminate.
	data := buildDictPDF(t, []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Count 1 /Kids [ 3 0 R ] >>",
		"<< /Type /Pages /Kids [ 2 0 R ] >>",
	})
	r, err := Open(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer r.Close()
	n, err := r.PageCount()
	if err != nil {
		t.Fatalf("PageCount: %v", err)
	}
	if n != 0 {
		t.Errorf("PageCount = %d, want 0", n)
	}
}

func TestPageCyclicParentTerminates(t *testing.T) {
	// The leaf page's /Parent points to itself; inheritance must terminate.
	data := buildDictPDF(t, []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Count 1 /Kids [ 3 0 R ] >>",
		"<< /Type /Page /Parent 3 0 R >>",
	})
	r, err := Open(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer r.Close()
	p, err := r.Page(0)
	if err != nil {
		t.Fatalf("Page(0): %v", err)
	}
	if box, ok := p.Box(MediaBox); ok {
		t.Errorf("MediaBox = %+v, want absent", box)
	}
	if rot := p.Rotation(); rot != 0 {
		t.Errorf("Rotation = %d, want 0", rot)
	}
	if _, ok := p.Resources(); ok {
		t.Errorf("Resources found, want none")
	}
}

func TestPagesMissingType(t *testing.T) {
	// Neither the intermediate node nor the leaf declares /Type; the walk
	// keys off /Kids presence instead.
	data := buildDictPDF(t, []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Kids [ 3 0 R ] /Count 1 >>",
		"<< /Parent 2 0 R /MediaBox [0 0 100 100] >>",
	})
	r, err := Open(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer r.Close()
	n, err := r.PageCount()
	if err != nil {
		t.Fatalf("PageCount: %v", err)
	}
	if n != 1 {
		t.Fatalf("PageCount = %d, want 1", n)
	}
	p, _ := r.Page(0)
	if box, ok := p.Box(MediaBox); !ok || box != (Rect{0, 0, 100, 100}) {
		t.Errorf("MediaBox = %+v ok=%v, want {0 0 100 100}", box, ok)
	}
}
