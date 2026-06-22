package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/speedata/pdfdisassembler"
)

// buildCyclicStructTreePDF builds a PDF whose /StructTreeRoot /K chain cycles
// (obj 5's /K points back to obj 4).
func buildCyclicStructTreePDF(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	off := func() int { return buf.Len() }
	fmt.Fprint(&buf, "%PDF-1.7\n%\xE2\xE3\xCF\xD3\n")
	offsets := make([]int, 6) // 1..5
	obj := func(n int, body string) {
		offsets[n] = off()
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", n, body)
	}
	obj(1, "<< /Type /Catalog /Pages 2 0 R /StructTreeRoot 3 0 R >>")
	obj(2, "<< /Type /Pages /Count 0 /Kids [] >>")
	obj(3, "<< /Type /StructTreeRoot /K 4 0 R >>")
	obj(4, "<< /Type /StructElem /S /Document /K 5 0 R >>")
	obj(5, "<< /Type /StructElem /S /P /K 4 0 R >>") // cycle back to obj 4

	xrefOff := off()
	fmt.Fprint(&buf, "xref\n0 6\n")
	fmt.Fprintf(&buf, "%010d %05d f \n", 0, 65535)
	for i := 1; i <= 5; i++ {
		fmt.Fprintf(&buf, "%010d %05d n \n", offsets[i], 0)
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size 6 /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", xrefOff)
	return buf.Bytes()
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	rp, wp, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = wp
	done := make(chan string, 1)
	go func() {
		var b bytes.Buffer
		io.Copy(&b, rp)
		done <- b.String()
	}()
	fn()
	wp.Close()
	os.Stdout = old
	return <-done
}

func TestWalkCyclicStructTreeTerminates(t *testing.T) {
	r, err := pdfdisassembler.Open(bytes.NewReader(buildCyclicStructTreePDF(t)))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer r.Close()
	cat, err := r.Catalog()
	if err != nil {
		t.Fatalf("Catalog: %v", err)
	}
	root, ok := cat.Dict("StructTreeRoot")
	if !ok {
		t.Fatal("no StructTreeRoot")
	}
	// Must return (the /K cycle would otherwise recurse until stack overflow);
	// the dump must show it descended the chain, proving the cycle is exercised.
	out := captureStdout(t, func() {
		walk(r, root, map[string]string{}, 0, map[pdfdisassembler.Reference]struct{}{})
	})
	for _, want := range []string{"Document", "P"} {
		if !strings.Contains(out, want) {
			t.Errorf("dump missing %q; got:\n%s", want, out)
		}
	}
}
