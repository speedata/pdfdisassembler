package filter

import (
	"bytes"
	"compress/zlib"
	"testing"
)

func TestASCIIHex(t *testing.T) {
	cases := map[string]string{
		"48656C6C6F>":   "Hello",
		"4 86 56C 6C6F": "Hello",
		"48656c6c6f":    "Hello",
	}
	for in, want := range cases {
		out, err := Decode("ASCIIHexDecode", []byte(in), Params{})
		if err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		if string(out) != want {
			t.Fatalf("%q: got %q want %q", in, out, want)
		}
	}
}

func TestASCII85(t *testing.T) {
	in := []byte("87cURD]i,\"Ebo80~>")
	out, err := Decode("ASCII85Decode", in, Params{})
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "Hello World!" {
		t.Fatalf("got %q", out)
	}
}

func TestRunLength(t *testing.T) {
	// 3 literal "ABC" (length-1=2), then 3 copies of 'X' (257-3=254), EOD.
	in := []byte{2, 'A', 'B', 'C', 254, 'X', 128}
	out, err := Decode("RunLengthDecode", in, Params{})
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "ABCXXX" {
		t.Fatalf("got %q", out)
	}
}

func TestFlate(t *testing.T) {
	var buf bytes.Buffer
	zw := zlib.NewWriter(&buf)
	zw.Write([]byte("hello flate"))
	zw.Close()
	out, err := Decode("FlateDecode", buf.Bytes(), Params{})
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "hello flate" {
		t.Fatalf("got %q", out)
	}
}

func TestFlatePNGPredictor(t *testing.T) {
	// 2 rows, 4 bytes each. Predictor tag 0 = None.
	row1 := []byte{0, 1, 2, 3, 4}
	row2 := []byte{0, 5, 6, 7, 8}
	raw := append(append([]byte{}, row1...), row2...)
	var buf bytes.Buffer
	zw := zlib.NewWriter(&buf)
	zw.Write(raw)
	zw.Close()
	out, err := Decode("FlateDecode", buf.Bytes(), Params{
		Predictor:        12,
		Columns:          4,
		Colors:           1,
		BitsPerComponent: 8,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	if !bytes.Equal(out, want) {
		t.Fatalf("got % x want % x", out, want)
	}
}

func TestFlateBombRejected(t *testing.T) {
	// 1 MiB of zeros (compresses to ~1 KB) against a 4 KB cap.
	var buf bytes.Buffer
	zw := zlib.NewWriter(&buf)
	zw.Write(make([]byte, 1<<20))
	zw.Close()
	if _, err := Decode("FlateDecode", buf.Bytes(), Params{MaxOutput: 4096}); err == nil {
		t.Fatal("expected error for output exceeding MaxOutput, got nil")
	}
}

func TestFlateUnderLimit(t *testing.T) {
	var buf bytes.Buffer
	zw := zlib.NewWriter(&buf)
	zw.Write([]byte("hello flate"))
	zw.Close()
	out, err := Decode("FlateDecode", buf.Bytes(), Params{MaxOutput: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "hello flate" {
		t.Fatalf("got %q", out)
	}
}

func TestRunLengthBombRejected(t *testing.T) {
	// {129,'X'} expands to 257-129 = 128 copies of 'X', past the 64-byte cap.
	if _, err := Decode("RunLengthDecode", []byte{129, 'X'}, Params{MaxOutput: 64}); err == nil {
		t.Fatal("expected error for output exceeding MaxOutput, got nil")
	}
}

func TestLZWDecodeAndBombRejected(t *testing.T) {
	// PDF-LZW (9-bit, MSB-first) codes 65,66,257 -> "AB" then EOD.
	in := []byte{0x20, 0x90, 0xA0, 0x20}
	out, err := Decode("LZWDecode", in, Params{})
	if err != nil || string(out) != "AB" {
		t.Fatalf("baseline decode: out=%q err=%v", out, err)
	}
	if _, err := Decode("LZWDecode", in, Params{MaxOutput: 1}); err == nil {
		t.Fatal("expected error for output exceeding MaxOutput, got nil")
	}
}

func TestImageFilterRejected(t *testing.T) {
	if !IsImageFilter("DCTDecode") {
		t.Fatal("DCTDecode should be image filter")
	}
	if !IsImageFilter("JPXDecode") {
		t.Fatal("JPXDecode should be image filter")
	}
	if IsImageFilter("FlateDecode") {
		t.Fatal("FlateDecode should not be image filter")
	}
}
