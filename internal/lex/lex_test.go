package lex

import (
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
