package filter

import (
	"bytes"
	"compress/lzw"
	"compress/zlib"
	"encoding/ascii85"
	"fmt"
	"testing"
)

// lcg fills b with a deterministic pseudo-random byte stream — enough entropy
// to grow the LZW dictionary through every code width and trigger a reset.
func lcg(b []byte, seed uint32) {
	x := seed
	for i := range b {
		x = x*1664525 + 1013904223
		b[i] = byte(x >> 24)
	}
}

// TestLZWRoundTripStdlib decodes streams produced by the standard library's
// MSB LZW writer. That writer uses the non-early code-width change, so decode
// with NoEarlyChange; the varied inputs exercise dictionary reuse, the KwKwK
// case, 9->12-bit width growth, and the dictionary-full reset.
func TestLZWRoundTripStdlib(t *testing.T) {
	big := make([]byte, 64<<10)
	lcg(big, 1)
	for _, orig := range [][]byte{
		[]byte("ABABABABABABABABABAB"),
		[]byte("TOBEORNOTTOBEORTOBEORNOT"),
		bytes.Repeat([]byte("xyz "), 4096),
		big,
		{},
		{42},
	} {
		var buf bytes.Buffer
		w := lzw.NewWriter(&buf, lzw.MSB, 8)
		if _, err := w.Write(orig); err != nil {
			t.Fatal(err)
		}
		w.Close()
		got, err := Decode("LZWDecode", buf.Bytes(), Params{NoEarlyChange: true})
		if err != nil {
			t.Fatalf("decode (len %d): %v", len(orig), err)
		}
		if !bytes.Equal(got, orig) {
			t.Fatalf("round-trip mismatch for len %d input", len(orig))
		}
	}
}

// TestLZWEarlyChangeHonored proves NoEarlyChange actually selects the decode
// convention: the stdlib stream (non-early) round-trips only with NoEarlyChange
// set; under the early-change default the same bytes must not reproduce the input.
func TestLZWEarlyChangeHonored(t *testing.T) {
	orig := make([]byte, 4096) // long enough to cross the first width boundary
	lcg(orig, 7)
	var buf bytes.Buffer
	w := lzw.NewWriter(&buf, lzw.MSB, 8)
	w.Write(orig)
	w.Close()
	enc := buf.Bytes()

	got, err := Decode("LZWDecode", enc, Params{NoEarlyChange: true})
	if err != nil || !bytes.Equal(got, orig) {
		t.Fatalf("NoEarlyChange decode of a non-early stream: err=%v equal=%v", err, bytes.Equal(got, orig))
	}
	if d, err := Decode("LZWDecode", enc, Params{}); err == nil && bytes.Equal(d, orig) {
		t.Fatal("early-change default reproduced a non-early stream: the flag is ignored")
	}
}

// A stream truncated mid-code must not panic: readBits pads the final partial
// word with zeros and decoding stops cleanly (partial output or error).
func TestLZWTruncatedNoPanic(t *testing.T) {
	orig := make([]byte, 600) // long enough to reach 10-bit codes
	lcg(orig, 3)
	var buf bytes.Buffer
	w := lzw.NewWriter(&buf, lzw.MSB, 8)
	w.Write(orig)
	w.Close()
	enc := buf.Bytes()
	for cut := 1; cut <= 4 && cut < len(enc); cut++ {
		_, _ = Decode("LZWDecode", enc[:len(enc)-cut], Params{NoEarlyChange: true})
	}
}

func TestASCII85RoundTripStdlib(t *testing.T) {
	for _, orig := range [][]byte{
		[]byte("Hello World!"),
		{0, 0, 0, 0, 1, 2, 3}, // includes an all-zero group
		{1},                   // 1-byte partial group
		{1, 2},                // 2-byte partial group
		{1, 2, 3},             // 3-byte partial group
		bytes.Repeat([]byte{255}, 17),
		{},
	} {
		enc := make([]byte, ascii85.MaxEncodedLen(len(orig)))
		n := ascii85.Encode(enc, orig)
		in := append(enc[:n:n], '~', '>')
		out, err := Decode("ASCII85Decode", in, Params{})
		if err != nil {
			t.Fatalf("decode %v: %v", orig, err)
		}
		if !bytes.Equal(out, orig) {
			t.Fatalf("round-trip mismatch: in=%v out=%v", orig, out)
		}
	}
}

func TestASCII85EdgeCases(t *testing.T) {
	// 'z' is shorthand for a full zero group; <~ is an optional opening marker;
	// whitespace between digits is ignored.
	out, err := Decode("ASCII85Decode", []byte("<~z 8 7 c U R D ] i , \" E b o 8 0 ~>"), Params{})
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if want := append([]byte{0, 0, 0, 0}, "Hello World!"...); !bytes.Equal(out, want) {
		t.Fatalf("got %q, want %q", out, want)
	}

	if _, err := Decode("ASCII85Decode", []byte("abc!\x01def~>"), Params{}); err == nil {
		t.Fatal("expected an error on a byte outside the ASCII85 alphabet")
	}
	// 'z' may not appear mid-group (after a partial digit).
	if _, err := Decode("ASCII85Decode", []byte("87z~>"), Params{}); err == nil {
		t.Fatal("expected an error for 'z' inside a group")
	}
}

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

func TestPredictorHostileParamsNoPanic(t *testing.T) {
	var buf bytes.Buffer
	zw := zlib.NewWriter(&buf)
	zw.Write([]byte("ABCDEFGH"))
	zw.Close()
	flate := buf.Bytes()

	cases := []struct {
		name string
		p    Params
	}{
		{"tiff_rowbytes_zero", Params{Predictor: 2, Colors: -7, BitsPerComponent: 1, Columns: 1}},
		{"png_stride_zero", Params{Predictor: 12, Colors: -15, BitsPerComponent: 1, Columns: 1}},
		{"png_negative_make", Params{Predictor: 12, Colors: -23, BitsPerComponent: 1, Columns: 1}},
		{"negative_columns", Params{Predictor: 12, Colors: 1, BitsPerComponent: 8, Columns: -4}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Decode("FlateDecode", flate, tc.p); err == nil {
				t.Fatal("expected an error for hostile predictor params, got nil")
			}
		})
	}
}

// pngForwardFilter is the inverse of applyPredictor's PNG path: it applies row
// filter tag to rows, so decode(encode(rows)) must recover rows exactly.
func pngForwardFilter(tag byte, rows [][]byte, bpp int) []byte {
	var out []byte
	prev := make([]byte, len(rows[0]))
	for _, raw := range rows {
		out = append(out, tag)
		filt := make([]byte, len(raw))
		for c := range raw {
			var left, upLeft byte
			up := prev[c]
			if c >= bpp {
				left = raw[c-bpp]
				upLeft = prev[c-bpp]
			}
			switch tag {
			case 0:
				filt[c] = raw[c]
			case 1:
				filt[c] = raw[c] - left
			case 2:
				filt[c] = raw[c] - up
			case 3:
				filt[c] = raw[c] - byte((int(left)+int(up))/2)
			case 4:
				filt[c] = raw[c] - paeth(left, up, upLeft)
			}
		}
		out = append(out, filt...)
		prev = raw
	}
	return out
}

func flate(t *testing.T, b []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zlib.NewWriter(&buf)
	zw.Write(b)
	zw.Close()
	return buf.Bytes()
}

func TestPredictorPNGRoundTrip(t *testing.T) {
	rows := [][]byte{
		{10, 20, 30, 40},
		{15, 25, 35, 45},
		{200, 100, 50, 25},
	}
	var want []byte
	for _, r := range rows {
		want = append(want, r...)
	}
	for tag := byte(0); tag <= 4; tag++ {
		t.Run(fmt.Sprintf("tag%d", tag), func(t *testing.T) {
			filtered := pngForwardFilter(tag, rows, 1)
			out, err := Decode("FlateDecode", flate(t, filtered), Params{
				Predictor: 12, Columns: 4, Colors: 1, BitsPerComponent: 8,
			})
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if !bytes.Equal(out, want) {
				t.Fatalf("tag %d round-trip: got % x want % x", tag, out, want)
			}
		})
	}
}

func TestPredictorTIFFRoundTrip(t *testing.T) {
	raw := []byte{10, 5, 250, 3}
	filt := make([]byte, len(raw))
	filt[0] = raw[0]
	for c := 1; c < len(raw); c++ {
		filt[c] = raw[c] - raw[c-1]
	}
	out, err := Decode("FlateDecode", flate(t, filt), Params{
		Predictor: 2, Columns: 4, Colors: 1, BitsPerComponent: 8,
	})
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !bytes.Equal(out, raw) {
		t.Fatalf("TIFF round-trip: got % x want % x", out, raw)
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

// FuzzDecode asserts every filter and the predictor never panic on arbitrary
// input or attacker-controlled predictor parameters.
func FuzzDecode(f *testing.F) {
	var seed bytes.Buffer
	zw := zlib.NewWriter(&seed)
	zw.Write([]byte("seed data"))
	zw.Close()
	f.Add(seed.Bytes(), 12, 4, 1, 8)
	f.Fuzz(func(t *testing.T, data []byte, predictor, columns, colors, bpc int) {
		p := Params{
			Predictor:        predictor,
			Columns:          columns,
			Colors:           colors,
			BitsPerComponent: bpc,
			MaxOutput:        1 << 20,
		}
		for _, name := range []string{"FlateDecode", "LZWDecode", "ASCII85Decode", "ASCIIHexDecode", "RunLengthDecode"} {
			_, _ = Decode(name, data, p)
		}
	})
}
