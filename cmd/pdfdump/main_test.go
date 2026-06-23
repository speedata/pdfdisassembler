package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// pdfdump is run on untrusted files, so hostile input must yield a graceful
// error or valid JSON — never a panic (which the test framework would surface).
func TestRunHostileInputsNoPanic(t *testing.T) {
	cases := map[string][]byte{
		"empty":         {},
		"garbage":       []byte("not a pdf at all"),
		"header only":   []byte("%PDF-1.7\n"),
		"broken xref":   []byte("%PDF-1.7\n1 0 obj\n<< /Type /Catalog >>\nendobj\nstartxref\n999999\n%%EOF"),
		"nul bytes":     bytes.Repeat([]byte{0}, 256),
		"unclosed dict": []byte("%PDF-1.7\n1 0 obj\n<< /Type /Catalog\nendobj\ntrailer\n<< /Root 1 0 R >>\nstartxref\n9\n%%EOF"),
	}
	dir := t.TempDir()
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			p := filepath.Join(dir, "in.pdf")
			if err := os.WriteFile(p, body, 0o600); err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			if err := run([]string{p}, &out); err == nil {
				if out.Len() == 0 || out.Bytes()[0] != '{' {
					t.Fatalf("succeeded but produced non-JSON output (%d bytes)", out.Len())
				}
			}
		})
	}
}

func TestRunValidFixtures(t *testing.T) {
	fixtures, err := filepath.Glob("../../testdata/fixtures/*/input.pdf")
	if err != nil || len(fixtures) == 0 {
		t.Fatalf("no fixtures found: %v", err)
	}
	for _, fx := range fixtures {
		t.Run(filepath.Base(filepath.Dir(fx)), func(t *testing.T) {
			var out bytes.Buffer
			if err := run([]string{"-stream-content", fx}, &out); err != nil {
				t.Fatalf("run: %v", err)
			}
			if out.Len() == 0 || out.Bytes()[0] != '{' {
				t.Fatalf("expected JSON output, got %d bytes", out.Len())
			}
		})
	}
}

func TestRunArgErrors(t *testing.T) {
	if err := run(nil, io.Discard); err == nil {
		t.Error("expected an error with no file argument")
	}
	if err := run([]string{"/nonexistent/nope.pdf"}, io.Discard); err == nil {
		t.Error("expected an error for a missing file")
	}
}
