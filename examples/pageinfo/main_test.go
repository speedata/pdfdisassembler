package main

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/speedata/pdfdisassembler"
)

// buildObjPDF assembles 1-based object bodies into a PDF with a classical xref
// and the given trailer dictionary body (without the surrounding << >>).
func buildObjPDF(t *testing.T, objs []string, trailer string) []byte {
	t.Helper()
	var buf bytes.Buffer
	off := func() int { return buf.Len() }
	fmt.Fprint(&buf, "%PDF-1.7\n%\xE2\xE3\xCF\xD3\n")
	offsets := make([]int, len(objs)+1)
	for i, body := range objs {
		offsets[i+1] = off()
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", i+1, body)
	}
	xrefOff := off()
	fmt.Fprintf(&buf, "xref\n0 %d\n%010d %05d f \n", len(objs)+1, 0, 65535)
	for i := 1; i <= len(objs); i++ {
		fmt.Fprintf(&buf, "%010d %05d n \n", offsets[i], 0)
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size %d %s >>\nstartxref\n%d\n%%%%EOF\n",
		len(objs)+1, trailer, xrefOff)
	return buf.Bytes()
}

// buildPagesPDF builds a two-page document: page 1 inherits its MediaBox and
// Resources from the /Pages root and carries a content stream; page 2 overrides
// MediaBox, adds a CropBox and /Rotate, and has no content.
func buildPagesPDF(t *testing.T) []byte {
	const content = "BT (Hi) Tj ET" // 13 bytes
	return buildObjPDF(t, []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Count 2 /Kids [ 3 0 R 4 0 R ] /MediaBox [0 0 612 792] /Resources << /Font << /F1 5 0 R >> >> >>",
		"<< /Type /Page /Parent 2 0 R /Contents 6 0 R >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] /CropBox [5 5 195 195] /Rotate 90 >>",
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content),
	}, "/Root 1 0 R")
}

func TestReport(t *testing.T) {
	r, err := pdfdisassembler.Open(bytes.NewReader(buildPagesPDF(t)))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer r.Close()

	var out bytes.Buffer
	if err := report(&out, r); err != nil {
		t.Fatalf("report: %v", err)
	}
	got := out.String()

	wants := []string{
		"Pages: 2",
		"Page 1:",
		"MediaBox: 612 x 792 pt", // inherited from /Pages root
		"Resources: [Font]",      // inherited
		"Content: 13 bytes decoded",
		"Page 2:",
		"MediaBox: 200 x 200 pt", // overridden locally
		"CropBox:  [5 5 195 195]",
		"Rotation: 90",
		"Content: 0 bytes decoded", // no /Contents
	}
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Errorf("output missing %q; got:\n%s", w, got)
		}
	}

	// Page 1 has no rotation, so no Rotation line should appear before "Page 2".
	page1 := got[strings.Index(got, "Page 1:"):strings.Index(got, "Page 2:")]
	if strings.Contains(page1, "Rotation:") {
		t.Errorf("page 1 should not print a Rotation line; got:\n%s", page1)
	}
}

// TestReportCyclicKidsTerminates feeds a page tree whose /Kids cycles back on
// itself; report must return rather than recurse until the stack overflows.
func TestReportCyclicKidsTerminates(t *testing.T) {
	data := buildObjPDF(t, []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Count 1 /Kids [ 3 0 R ] >>",
		"<< /Type /Pages /Kids [ 2 0 R ] >>", // cycle back to obj 2
	}, "/Root 1 0 R")
	r, err := pdfdisassembler.Open(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer r.Close()

	var out bytes.Buffer
	if err := report(&out, r); err != nil {
		t.Fatalf("report: %v", err)
	}
	if got := out.String(); !strings.Contains(got, "Pages: 0") {
		t.Errorf("want \"Pages: 0\", got:\n%s", got)
	}
}
