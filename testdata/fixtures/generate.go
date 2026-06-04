//go:build ignore

// Run from the repo root:
//
//	go run testdata/fixtures/generate.go
//
// (re)creates synthetic input.pdf files for the fixtures that we author
// in code (rather than dropping in real-world samples). After running,
// refresh goldens with:
//
//	go test -update -run TestFixtures
//
// Then inspect the resulting golden.json before committing.
package main

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	write("minimal", minimalPDF())
	write("xref-stream", xrefStreamPDF())
	write("flate-stream", flateStreamPDF())
}

func write(name string, data []byte) {
	dir := filepath.Join("testdata/fixtures", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		panic(err)
	}
	path := filepath.Join(dir, "input.pdf")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		panic(err)
	}
	fmt.Printf("wrote %s (%d bytes)\n", path, len(data))
}

func minimalPDF() []byte {
	var buf bytes.Buffer
	off := func() int { return buf.Len() }
	fmt.Fprint(&buf, "%PDF-1.7\n%\xE2\xE3\xCF\xD3\n")

	offsets := make([]int, 5)
	offsets[1] = off()
	fmt.Fprint(&buf, "1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n")
	offsets[2] = off()
	fmt.Fprint(&buf, "2 0 obj\n<< /Type /Pages /Count 1 /Kids [ 3 0 R ] >>\nendobj\n")
	offsets[3] = off()
	fmt.Fprint(&buf, "3 0 obj\n<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] >>\nendobj\n")
	offsets[4] = off()
	fmt.Fprint(&buf, "4 0 obj\n<< /Title (Hello) /Producer (pdfdisassembler-fixture) >>\nendobj\n")

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

func xrefStreamPDF() []byte {
	var buf bytes.Buffer
	off := func() int { return buf.Len() }
	fmt.Fprint(&buf, "%PDF-2.0\n%\xE2\xE3\xCF\xD3\n")

	offsets := make([]int, 3)
	offsets[1] = off()
	fmt.Fprint(&buf, "1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n")
	offsets[2] = off()
	fmt.Fprint(&buf, "2 0 obj\n<< /Type /Pages /Count 0 /Kids [] >>\nendobj\n")

	rows := []byte{}
	add := func(typ, f1, f2 uint64) {
		rows = append(rows, byte(typ))
		rows = append(rows, byte(f1>>16), byte(f1>>8), byte(f1))
		rows = append(rows, byte(f2))
	}
	add(0, 0, 0xFFFF)
	add(1, uint64(offsets[1]), 0)
	add(1, uint64(offsets[2]), 0)

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

func flateStreamPDF() []byte {
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
