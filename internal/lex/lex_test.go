package lex

import (
	"bytes"
	"testing"
)

func TestLexerNamesAndNumbers(t *testing.T) {
	src := []byte("/Length 12 -3 +4 5.6 .7 8. true false null")
	lx := New(src)
	want := []struct {
		kind  Kind
		bytes string
	}{
		{Name, "Length"},
		{Integer, "12"},
		{Integer, "-3"},
		{Integer, "+4"},
		{Real, "5.6"},
		{Real, ".7"},
		{Real, "8."},
		{Keyword, "true"},
		{Keyword, "false"},
		{Keyword, "null"},
		{EOF, ""},
	}
	for i, w := range want {
		tok, err := lx.Next()
		if err != nil {
			t.Fatalf("tok %d: err %v", i, err)
		}
		if tok.Kind != w.kind {
			t.Fatalf("tok %d: kind %v want %v", i, tok.Kind, w.kind)
		}
		if string(tok.Bytes) != w.bytes {
			t.Fatalf("tok %d: bytes %q want %q", i, tok.Bytes, w.bytes)
		}
	}
}

func TestLexerNameHashEscape(t *testing.T) {
	src := []byte("/A#20B /ABC")
	lx := New(src)
	t1, _ := lx.Next()
	if string(t1.Bytes) != "A B" {
		t.Fatalf("got %q want %q", t1.Bytes, "A B")
	}
	t2, _ := lx.Next()
	if string(t2.Bytes) != "ABC" {
		t.Fatalf("got %q want %q", t2.Bytes, "ABC")
	}
}

func TestLexerLiteralString(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"(hello)", "hello"},
		{"(a (nested) b)", "a (nested) b"},
		{"(line\\nbreak)", "line\nbreak"},
		{"(\\053\\053)", "++"},
		{"(\\\\)", "\\"},
		{"(a\\\nb)", "ab"},
	}
	for _, c := range cases {
		lx := New([]byte(c.in))
		tok, err := lx.Next()
		if err != nil {
			t.Fatalf("%q: %v", c.in, err)
		}
		if tok.Kind != LitString {
			t.Fatalf("%q: kind %v", c.in, tok.Kind)
		}
		if string(tok.Bytes) != c.want {
			t.Fatalf("%q: got %q want %q", c.in, tok.Bytes, c.want)
		}
	}
}

func TestLexerHexString(t *testing.T) {
	src := []byte("<48656C6C6F>")
	tok, err := New(src).Next()
	if err != nil {
		t.Fatal(err)
	}
	if tok.Kind != HexString {
		t.Fatalf("kind %v", tok.Kind)
	}
	if string(tok.Bytes) != "Hello" {
		t.Fatalf("got %q", tok.Bytes)
	}
}

func TestLexerHexStringOddNibble(t *testing.T) {
	src := []byte("<F>")
	tok, err := New(src).Next()
	if err != nil {
		t.Fatal(err)
	}
	if len(tok.Bytes) != 1 || tok.Bytes[0] != 0xF0 {
		t.Fatalf("got % x", tok.Bytes)
	}
}

func TestLexerDictArrayDelims(t *testing.T) {
	src := []byte("<< /A 1 >> [ 1 2 3 ]")
	lx := New(src)
	kinds := []Kind{DictStart, Name, Integer, DictEnd, ArrayStart, Integer, Integer, Integer, ArrayEnd, EOF}
	for i, k := range kinds {
		tok, err := lx.Next()
		if err != nil {
			t.Fatalf("tok %d: %v", i, err)
		}
		if tok.Kind != k {
			t.Fatalf("tok %d: kind %v want %v", i, tok.Kind, k)
		}
	}
}

func TestLexerComment(t *testing.T) {
	src := []byte("% comment\n1 % trailing\n2")
	lx := New(src)
	t1, _ := lx.Next()
	t2, _ := lx.Next()
	t3, _ := lx.Next()
	if t1.Kind != Integer || string(t1.Bytes) != "1" {
		t.Fatalf("t1: %v %q", t1.Kind, t1.Bytes)
	}
	if t2.Kind != Integer || string(t2.Bytes) != "2" {
		t.Fatalf("t2: %v %q", t2.Kind, t2.Bytes)
	}
	if t3.Kind != EOF {
		t.Fatalf("t3 kind %v", t3.Kind)
	}
}

func TestReadStreamDataHostileLength(t *testing.T) {
	const maxInt = int(^uint(0) >> 1)
	for _, length := range []int{-1, -1000, maxInt} {
		// Leading "\n" makes the EOL skip advance pos, so pos+length overflows.
		l := New([]byte("\nstream body bytes"))
		if _, err := l.ReadStreamData(length); err == nil {
			t.Fatalf("length=%d: expected error, got nil", length)
		}
	}
}

func TestReadStreamDataValid(t *testing.T) {
	l := New([]byte("\nABCDEF"))
	out, err := l.ReadStreamData(6)
	if err != nil {
		t.Fatalf("ReadStreamData: %v", err)
	}
	if string(out) != "ABCDEF" {
		t.Fatalf("got %q want ABCDEF", out)
	}
}

func TestLexerTruncatedTokensError(t *testing.T) {
	cases := map[string]string{
		"name escape at eof":      "/AB#",
		"name escape one digit":   "/AB#F",
		"name escape bad hex":     "/A#GG",
		"string backslash at eof": "(abc\\",
		"unterminated string":     "(abc",
		"unterminated hex":        "<48",
		"bad hex digit":           "<4G>",
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := New([]byte(src)).Next(); err == nil {
				t.Fatalf("%q: expected error, got nil", src)
			}
		})
	}
}

func TestLexerStringEscapes(t *testing.T) {
	lx := New([]byte(`(\101\n\)\(end)`)) // octal 'A', newline, literal ) and (
	tok, err := lx.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if want := "A\n)(end"; string(tok.Bytes) != want {
		t.Fatalf("got %q want %q", tok.Bytes, want)
	}
}

// FuzzLexer asserts tokenisation never panics on arbitrary input.
func FuzzLexer(f *testing.F) {
	f.Add([]byte("/Name 123 -4.5 (str\\n) <48656C> << /A 1 >> [ 1 2 ] true null %c"))
	f.Fuzz(func(t *testing.T, data []byte) {
		lx := New(data)
		for i := 0; i <= len(data); i++ {
			tok, err := lx.Next()
			if err != nil || tok.Kind == EOF {
				break
			}
		}
	})
}

// Position accessors that back the xref reader's manual seeking.
func TestLexerAccessors(t *testing.T) {
	src := []byte("hello world")
	lx := New(src)
	if lx.Pos() != 0 {
		t.Errorf("initial Pos = %d, want 0", lx.Pos())
	}
	if !bytes.Equal(lx.Source(), src) {
		t.Errorf("Source = %q, want %q", lx.Source(), src)
	}
	lx.SetPos(6)
	if lx.Pos() != 6 {
		t.Errorf("Pos after SetPos(6) = %d, want 6", lx.Pos())
	}
	if got := lx.Remaining(); string(got) != "world" {
		t.Errorf("Remaining = %q, want world", got)
	}
}

// Escape and newline handling in literal strings (§7.3.4.2) beyond the cases in
// TestLexerLiteralString: the \r\t\b\f escapes, backslash-CR/CRLF continuations,
// unknown escapes (backslash dropped), and bare CR/CRLF normalised to LF.
func TestLexerLiteralStringEscapes(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"named_escapes", "(\\t\\b\\f\\r)", "\t\b\f\r"},
		{"cr_line_continuation", "(a\\\rb)", "ab"},
		{"crlf_line_continuation", "(a\\\r\nb)", "ab"},
		{"unknown_escape_keeps_char", "(\\q)", "q"},
		{"bare_cr_to_lf", "(a\rb)", "a\nb"},
		{"bare_crlf_to_lf", "(a\r\nb)", "a\nb"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tok, err := New([]byte(c.in)).Next()
			if err != nil {
				t.Fatalf("Next: %v", err)
			}
			if tok.Kind != LitString {
				t.Fatalf("kind = %v, want LitString", tok.Kind)
			}
			if string(tok.Bytes) != c.want {
				t.Errorf("got %q, want %q", tok.Bytes, c.want)
			}
		})
	}
}

// A trailing backslash and an unterminated string must error, not read past the
// buffer.
func TestLexerLiteralStringErrors(t *testing.T) {
	for _, in := range []string{"(\\", "(abc"} {
		if _, err := New([]byte(in)).Next(); err == nil {
			t.Errorf("%q: expected an error, got nil", in)
		}
	}
}

// IsDelimiter / IsWhitespace partition the special bytes per §7.2.2; a
// misclassified byte would break tokenisation, so pin the delimiter set.
func TestIsDelimiter(t *testing.T) {
	for _, c := range []byte("()<>[]{}/%") {
		if !IsDelimiter(c) {
			t.Errorf("IsDelimiter(%q) = false, want true", c)
		}
	}
	for _, c := range []byte("aZ0 \t.\\") {
		if IsDelimiter(c) {
			t.Errorf("IsDelimiter(%q) = true, want false", c)
		}
	}
}

func TestHexDigit(t *testing.T) {
	valid := map[byte]int{'0': 0, '9': 9, 'a': 10, 'f': 15, 'A': 10, 'F': 15}
	for c, want := range valid {
		if v, ok := hexDigit(c); !ok || v != want {
			t.Errorf("hexDigit(%q) = (%d, %v), want (%d, true)", c, v, ok, want)
		}
	}
	for _, c := range []byte("gG/ \x00") {
		if _, ok := hexDigit(c); ok {
			t.Errorf("hexDigit(%q) = ok, want not-ok", c)
		}
	}
}
