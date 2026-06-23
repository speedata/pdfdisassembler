package pdfdisassembler

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/speedata/pdfdisassembler/internal/lex"
)

// buildPDFWithObjectBody puts body as object 3 in a minimal classical-xref PDF.
func buildPDFWithObjectBody(t *testing.T, body string) []byte {
	t.Helper()
	var buf bytes.Buffer
	off := func() int { return buf.Len() }
	fmt.Fprint(&buf, "%PDF-1.7\n%\xE2\xE3\xCF\xD3\n")
	offsets := make([]int, 4)
	offsets[1] = off()
	fmt.Fprint(&buf, "1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n")
	offsets[2] = off()
	fmt.Fprint(&buf, "2 0 obj\n<< /Type /Pages /Count 0 /Kids [] >>\nendobj\n")
	offsets[3] = off()
	fmt.Fprintf(&buf, "3 0 obj\n%s\nendobj\n", body)
	xrefOff := off()
	fmt.Fprint(&buf, "xref\n0 4\n")
	fmt.Fprintf(&buf, "%010d %05d f \n", 0, 65535)
	for i := 1; i <= 3; i++ {
		fmt.Fprintf(&buf, "%010d %05d n \n", offsets[i], 0)
	}
	fmt.Fprint(&buf, "trailer\n<< /Size 4 /Root 1 0 R >>\n")
	fmt.Fprintf(&buf, "startxref\n%d\n%%%%EOF\n", xrefOff)
	return buf.Bytes()
}

func TestDeeplyNestedRejected(t *testing.T) {
	// Far above the parser's depth cap, but well below a real stack overflow.
	const depth = 2000
	tests := []struct{ name, body string }{
		{"array", strings.Repeat("[", depth) + strings.Repeat("]", depth)},
		{"dict", strings.Repeat("<< /K ", depth) + "0" + strings.Repeat(" >>", depth)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := buildPDFWithObjectBody(t, tt.body)
			r, err := Open(bytes.NewReader(data))
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			defer r.Close()
			if _, err := r.Resolve(Reference{Number: 3, Generation: 0}); err == nil {
				t.Fatal("expected error for over-deep nesting")
			}
		})
	}
}

func TestModeratelyNestedArrayResolves(t *testing.T) {
	data := buildPDFWithObjectBody(t, strings.Repeat("[", 100)+strings.Repeat("]", 100))
	r, err := Open(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer r.Close()
	obj, err := r.Resolve(Reference{Number: 3, Generation: 0})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if _, ok := obj.(Array); !ok {
		t.Fatalf("got %T, want Array", obj)
	}
}

// Malformed token streams must error, never panic (cases labelled inline).
func TestParseObjectErrors(t *testing.T) {
	for _, src := range []string{
		"",          // EOF where an object is expected
		"]",         // stray ArrayEnd
		">>",        // stray DictEnd
		"foo",       // unexpected keyword
		"[ 1 2",     // unterminated array
		"<< /K 1",   // unterminated dict
		"<< 1 2 >>", // dict key is not a name
	} {
		t.Run(src, func(t *testing.T) {
			p := newParser(lex.New([]byte(src)), nil)
			if _, err := p.parseObject(); err == nil {
				t.Fatal("expected an error, got nil")
			}
		})
	}
}
