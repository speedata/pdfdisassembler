package pdfdisassembler

import (
	"testing"
	"time"
	"unicode/utf16"
)

// Round-trip via utf16.Encode (the independent inverse): the emoji forces a
// surrogate pair, and both byte orders dispatch off their BOM.
func TestDecodeTextStringUTF16RoundTrip(t *testing.T) {
	const s = "Hello, 世界 \U0001F600 é"
	u16 := utf16.Encode([]rune(s))
	be := []byte{0xFE, 0xFF}
	le := []byte{0xFF, 0xFE}
	for _, v := range u16 {
		be = append(be, byte(v>>8), byte(v))
		le = append(le, byte(v), byte(v>>8))
	}
	if got := decodeTextString(be); got != s {
		t.Errorf("UTF-16BE: got %q want %q", got, s)
	}
	if got := decodeTextString(le); got != s {
		t.Errorf("UTF-16LE: got %q want %q", got, s)
	}
}

// A UTF-16 payload with an odd byte count must drop the dangling byte, not
// read past the end — for both byte orders.
func TestDecodeUTF16OddLengthNoPanic(t *testing.T) {
	if got := decodeTextString([]byte{0xFE, 0xFF, 0x00, 0x41, 0x00}); got != "A" {
		t.Errorf("UTF-16BE: got %q, want A", got)
	}
	if got := decodeTextString([]byte{0xFF, 0xFE, 0x41, 0x00, 0x00}); got != "A" {
		t.Errorf("UTF-16LE: got %q, want A", got)
	}
}

func TestDecodeTextStringDispatch(t *testing.T) {
	// UTF-8 BOM (PDF 2.0): bytes after the BOM are returned verbatim.
	if got := decodeTextString([]byte{0xEF, 0xBB, 0xBF, 'h', 'i'}); got != "hi" {
		t.Errorf("UTF-8 BOM: got %q want hi", got)
	}
	// No BOM: PDFDocEncoding, which is ASCII over 0x20-0x7E.
	if got := decodeTextString([]byte("ASCII")); got != "ASCII" {
		t.Errorf("PDFDocEncoding ASCII: got %q", got)
	}
}

// Spot-check the PDFDocEncoding table (PDF 32000-1:2008 Annex D.2), including
// the high-range remaps and an undefined slot that must become U+FFFD.
func TestDecodePDFDocEncoding(t *testing.T) {
	cases := map[byte]rune{
		0x41: 'A',
		0x18: '˘', // breve
		0x80: '•', // bullet
		0xA0: '€', // euro sign
		0xE9: 'é', // Latin-1 range, identity-mapped
		0x7F: '�', // undefined
		0x9F: '�', // undefined
	}
	for b, want := range cases {
		got := []rune(decodeTextString([]byte{b}))
		if len(got) != 1 || got[0] != want {
			t.Errorf("byte 0x%02X decoded to %q, want %q", b, string(got), string(want))
		}
	}
}

func TestParseDate(t *testing.T) {
	utc := func(y int, mo time.Month, d, h, mi, s int) time.Time {
		return time.Date(y, mo, d, h, mi, s, 0, time.UTC)
	}
	tests := []struct {
		in   string
		want time.Time
	}{
		{"D:20201231235959Z", utc(2020, 12, 31, 23, 59, 59)},
		{"D:20200229", utc(2020, 2, 29, 0, 0, 0)}, // leap day
		{"D:2020", utc(2020, 1, 1, 0, 0, 0)},      // year only, defaults fill in
		{"20200115", utc(2020, 1, 15, 0, 0, 0)},   // optional D: prefix omitted
		{"", time.Time{}},
		{"garbage", time.Time{}},
		{"D:20201340", time.Time{}}, // month 13 rejected
	}
	for _, tt := range tests {
		if got := parseDate(tt.in); !got.Equal(tt.want) {
			t.Errorf("parseDate(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
	// Signed timezone offset, with the apostrophe separator.
	got := parseDate("D:20200101120000+05'30'")
	want := time.Date(2020, 1, 1, 12, 0, 0, 0, time.FixedZone("", 5*3600+30*60))
	if !got.Equal(want) {
		t.Errorf("tz parse = %v, want %v", got, want)
	}
}

// The dump heuristic for rendering a string inline vs hex-escaped. The
// high-byte cases are the subtle ones: 0x80 is a clean PDFDocEncoding glyph
// (bullet), 0x9F an undefined slot.
func TestLooksLikeText(t *testing.T) {
	cases := []struct {
		name string
		in   String
		want bool
	}{
		{"utf16be_bom", String{0xFE, 0xFF, 0, 'A'}, true},
		{"utf16le_bom", String{0xFF, 0xFE, 'A', 0}, true},
		{"utf8_bom", String{0xEF, 0xBB, 0xBF, 'h', 'i'}, true},
		{"ascii", String("Hello, World!"), true},
		{"ascii_with_whitespace", String("a\tb\r\nc"), true},
		{"control_byte", String{'a', 0x01, 'b'}, false},
		{"del_byte", String{0x7F}, false},
		{"pdfdoc_high_clean", String{0x80}, true},
		{"pdfdoc_high_undefined", String{0x9F}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := looksLikeText(c.in); got != c.want {
				t.Errorf("looksLikeText(% x) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}
