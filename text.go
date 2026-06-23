package pdfdisassembler

import (
	"encoding/binary"
	"strings"
	"time"
	"unicode/utf16"
)

// decodeTextString decodes b according to the PDF text-string convention
// (PDF 32000-1:2008 §7.9.2.2): UTF-16BE with BOM, UTF-8 with BOM (PDF 2.0),
// otherwise PDFDocEncoding.
func decodeTextString(b []byte) string {
	switch {
	case len(b) >= 2 && b[0] == 0xFE && b[1] == 0xFF:
		return decodeUTF16BE(b[2:])
	case len(b) >= 2 && b[0] == 0xFF && b[1] == 0xFE:
		// UTF-16LE: not spec'd for PDF text strings but observed in the
		// wild from misbehaving producers; decode rather than mojibake.
		return decodeUTF16LE(b[2:])
	case len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF:
		return string(b[3:])
	default:
		return decodePDFDocEncoding(b)
	}
}

func decodeUTF16BE(b []byte) string {
	if len(b)%2 != 0 {
		b = b[:len(b)-1]
	}
	u16 := make([]uint16, len(b)/2)
	for i := range u16 {
		u16[i] = binary.BigEndian.Uint16(b[2*i:])
	}
	return string(utf16.Decode(u16))
}

func decodeUTF16LE(b []byte) string {
	if len(b)%2 != 0 {
		b = b[:len(b)-1]
	}
	u16 := make([]uint16, len(b)/2)
	for i := range u16 {
		u16[i] = binary.LittleEndian.Uint16(b[2*i:])
	}
	return string(utf16.Decode(u16))
}

// decodePDFDocEncoding maps each byte through the PDFDocEncoding table
// from PDF 32000-1:2008 Annex D.2.
func decodePDFDocEncoding(b []byte) string {
	var sb strings.Builder
	sb.Grow(len(b))
	for _, c := range b {
		r := pdfDocEncoding[c]
		if r == 0xFFFD {
			// Undefined slot — emit replacement character.
			sb.WriteRune('�')
		} else {
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

// pdfDocEncoding is the PDFDocEncoding to Unicode mapping (256 entries).
// Slots without a Unicode mapping are 0xFFFD.
var pdfDocEncoding = [256]rune{
	// 0x00–0x17 (control characters mostly unused in PDFDocEncoding)
	0x0000, 0xFFFD, 0xFFFD, 0xFFFD, 0xFFFD, 0xFFFD, 0xFFFD, 0xFFFD,
	0x0008, 0x0009, 0x000A, 0xFFFD, 0x000C, 0x000D, 0xFFFD, 0xFFFD,
	0xFFFD, 0xFFFD, 0xFFFD, 0xFFFD, 0xFFFD, 0xFFFD, 0xFFFD, 0xFFFD,
	// 0x18–0x1F
	0x02D8, 0x02C7, 0x02C6, 0x02D9, 0x02DD, 0x02DB, 0x02DA, 0x02DC,
	// 0x20–0x7E identical to ASCII
	0x0020, 0x0021, 0x0022, 0x0023, 0x0024, 0x0025, 0x0026, 0x0027,
	0x0028, 0x0029, 0x002A, 0x002B, 0x002C, 0x002D, 0x002E, 0x002F,
	0x0030, 0x0031, 0x0032, 0x0033, 0x0034, 0x0035, 0x0036, 0x0037,
	0x0038, 0x0039, 0x003A, 0x003B, 0x003C, 0x003D, 0x003E, 0x003F,
	0x0040, 0x0041, 0x0042, 0x0043, 0x0044, 0x0045, 0x0046, 0x0047,
	0x0048, 0x0049, 0x004A, 0x004B, 0x004C, 0x004D, 0x004E, 0x004F,
	0x0050, 0x0051, 0x0052, 0x0053, 0x0054, 0x0055, 0x0056, 0x0057,
	0x0058, 0x0059, 0x005A, 0x005B, 0x005C, 0x005D, 0x005E, 0x005F,
	0x0060, 0x0061, 0x0062, 0x0063, 0x0064, 0x0065, 0x0066, 0x0067,
	0x0068, 0x0069, 0x006A, 0x006B, 0x006C, 0x006D, 0x006E, 0x006F,
	0x0070, 0x0071, 0x0072, 0x0073, 0x0074, 0x0075, 0x0076, 0x0077,
	0x0078, 0x0079, 0x007A, 0x007B, 0x007C, 0x007D, 0x007E, 0xFFFD,
	// 0x80–0x9F: punctuation and symbol additions per PDFDocEncoding
	0x2022, 0x2020, 0x2021, 0x2026, 0x2014, 0x2013, 0x0192, 0x2044,
	0x2039, 0x203A, 0x2212, 0x2030, 0x201E, 0x201C, 0x201D, 0x2018,
	0x2019, 0x201A, 0x2122, 0xFB01, 0xFB02, 0x0141, 0x0152, 0x0160,
	0x0178, 0x017D, 0x0131, 0x0142, 0x0153, 0x0161, 0x017E, 0xFFFD,
	// 0xA0
	0x20AC,
	// 0xA1–0xFF: same as ISO Latin-1 / Unicode 0x00A1–0x00FF, except a
	// few slots marked undefined by the spec.
	0x00A1, 0x00A2, 0x00A3, 0x00A4, 0x00A5, 0x00A6, 0x00A7,
	0x00A8, 0x00A9, 0x00AA, 0x00AB, 0x00AC, 0xFFFD, 0x00AE, 0x00AF,
	0x00B0, 0x00B1, 0x00B2, 0x00B3, 0x00B4, 0x00B5, 0x00B6, 0x00B7,
	0x00B8, 0x00B9, 0x00BA, 0x00BB, 0x00BC, 0x00BD, 0x00BE, 0x00BF,
	0x00C0, 0x00C1, 0x00C2, 0x00C3, 0x00C4, 0x00C5, 0x00C6, 0x00C7,
	0x00C8, 0x00C9, 0x00CA, 0x00CB, 0x00CC, 0x00CD, 0x00CE, 0x00CF,
	0x00D0, 0x00D1, 0x00D2, 0x00D3, 0x00D4, 0x00D5, 0x00D6, 0x00D7,
	0x00D8, 0x00D9, 0x00DA, 0x00DB, 0x00DC, 0x00DD, 0x00DE, 0x00DF,
	0x00E0, 0x00E1, 0x00E2, 0x00E3, 0x00E4, 0x00E5, 0x00E6, 0x00E7,
	0x00E8, 0x00E9, 0x00EA, 0x00EB, 0x00EC, 0x00ED, 0x00EE, 0x00EF,
	0x00F0, 0x00F1, 0x00F2, 0x00F3, 0x00F4, 0x00F5, 0x00F6, 0x00F7,
	0x00F8, 0x00F9, 0x00FA, 0x00FB, 0x00FC, 0x00FD, 0x00FE, 0x00FF,
}

// parseDate parses a PDF date string of the form
// "D:YYYYMMDDHHmmSSOHH'mm'" or any shorter prefix. Returns the zero time
// if the input is empty or unparseable.
func parseDate(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	s = strings.TrimPrefix(s, "D:")
	// Defaults per spec: month/day = 01, time = 00, offset = UTC.
	year, month, day := 0, 1, 1
	hour, minute, second := 0, 0, 0
	tzSign := byte('Z')
	tzHour, tzMinute := 0, 0

	read := func(n int) (int, bool) {
		if len(s) < n {
			return 0, false
		}
		v := 0
		for i := 0; i < n; i++ {
			c := s[i]
			if c < '0' || c > '9' {
				return 0, false
			}
			v = v*10 + int(c-'0')
		}
		s = s[n:]
		return v, true
	}

	if v, ok := read(4); ok {
		year = v
	} else {
		return time.Time{}
	}
	if v, ok := read(2); ok {
		month = v
	}
	if v, ok := read(2); ok {
		day = v
	}
	if v, ok := read(2); ok {
		hour = v
	}
	if v, ok := read(2); ok {
		minute = v
	}
	if v, ok := read(2); ok {
		second = v
	}

	if len(s) > 0 {
		switch s[0] {
		case '+', '-', 'Z':
			tzSign = s[0]
			s = s[1:]
		}
	}
	if tzSign != 'Z' {
		if v, ok := read(2); ok {
			tzHour = v
		}
		// Optional apostrophe between hour and minute.
		s = strings.TrimPrefix(s, "'")
		if v, ok := read(2); ok {
			tzMinute = v
		}
	}

	loc := time.UTC
	if tzSign == '+' || tzSign == '-' {
		off := tzHour*3600 + tzMinute*60
		if tzSign == '-' {
			off = -off
		}
		loc = time.FixedZone("", off)
	}

	if month < 1 || month > 12 || day < 1 || day > 31 {
		return time.Time{}
	}
	return time.Date(year, time.Month(month), day, hour, minute, second, 0, loc)
}
