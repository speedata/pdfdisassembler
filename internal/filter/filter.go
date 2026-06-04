// Package filter implements the PDF stream filters needed for read-only
// inspection of document structure: FlateDecode (with predictors), LZW,
// ASCII85, ASCIIHex, RunLength.
//
// Image-only filters (DCTDecode, JBIG2Decode, JPXDecode, CCITTFaxDecode)
// are intentionally not implemented — pdfdisassembler does not decode
// image streams.
package filter

import (
	"bytes"
	"compress/zlib"
	"errors"
	"fmt"
	"io"
)

// ErrUnsupported is returned for filters this package does not implement.
type ErrUnsupported struct{ Name string }

func (e ErrUnsupported) Error() string {
	return fmt.Sprintf("pdfdisassembler/filter: unsupported filter %q", e.Name)
}

// Params describes the decode-time parameters for a single filter.
type Params struct {
	// FlateDecode/LZWDecode predictor parameters.
	Predictor        int
	Columns          int
	Colors           int
	BitsPerComponent int
	// LZWDecode early-change flag.
	EarlyChange int
}

// Decode applies the named filter to in.
func Decode(name string, in []byte, p Params) ([]byte, error) {
	switch name {
	case "FlateDecode", "Fl":
		return decodeFlate(in, p)
	case "LZWDecode", "LZW":
		return decodeLZW(in, p)
	case "ASCII85Decode", "A85":
		return decodeASCII85(in)
	case "ASCIIHexDecode", "AHx":
		return decodeASCIIHex(in)
	case "RunLengthDecode", "RL":
		return decodeRunLength(in)
	}
	return nil, ErrUnsupported{Name: name}
}

// IsImageFilter reports whether name designates one of the image-only
// filters that pdfdisassembler intentionally skips.
func IsImageFilter(name string) bool {
	switch name {
	case "DCTDecode", "DCT",
		"JBIG2Decode",
		"JPXDecode",
		"CCITTFaxDecode", "CCF",
		"Crypt":
		return true
	}
	return false
}

func decodeFlate(in []byte, p Params) ([]byte, error) {
	zr, err := zlib.NewReader(bytes.NewReader(in))
	if err != nil {
		return nil, fmt.Errorf("FlateDecode: %w", err)
	}
	defer zr.Close()
	dec, err := io.ReadAll(zr)
	if err != nil {
		return nil, fmt.Errorf("FlateDecode: %w", err)
	}
	if p.Predictor > 1 {
		return applyPredictor(dec, p)
	}
	return dec, nil
}

func decodeASCII85(in []byte) ([]byte, error) {
	// Trim "<~" prefix and "~>" suffix if present.
	if len(in) >= 2 && in[0] == '<' && in[1] == '~' {
		in = in[2:]
	}
	end := bytes.Index(in, []byte("~>"))
	if end >= 0 {
		in = in[:end]
	}

	var out []byte
	var group uint32
	n := 0
	for _, c := range in {
		switch {
		case c == 'z':
			if n != 0 {
				return nil, errors.New("ASCII85Decode: 'z' inside group")
			}
			out = append(out, 0, 0, 0, 0)
			continue
		case c >= '!' && c <= 'u':
			group = group*85 + uint32(c-'!')
			n++
		case c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == '\f':
			continue
		default:
			return nil, fmt.Errorf("ASCII85Decode: invalid byte 0x%02x", c)
		}
		if n == 5 {
			out = append(out,
				byte(group>>24),
				byte(group>>16),
				byte(group>>8),
				byte(group),
			)
			group = 0
			n = 0
		}
	}
	if n > 0 {
		for i := n; i < 5; i++ {
			group = group*85 + 84
		}
		buf := []byte{
			byte(group >> 24),
			byte(group >> 16),
			byte(group >> 8),
			byte(group),
		}
		out = append(out, buf[:n-1]...)
	}
	return out, nil
}

func decodeASCIIHex(in []byte) ([]byte, error) {
	var out []byte
	var hi int
	have := false
	for _, c := range in {
		if c == '>' {
			break
		}
		if c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == '\f' {
			continue
		}
		d, ok := hexDigit(c)
		if !ok {
			return nil, fmt.Errorf("ASCIIHexDecode: invalid hex byte 0x%02x", c)
		}
		if have {
			out = append(out, byte(hi<<4|d))
			have = false
		} else {
			hi = d
			have = true
		}
	}
	if have {
		out = append(out, byte(hi<<4))
	}
	return out, nil
}

func decodeRunLength(in []byte) ([]byte, error) {
	var out []byte
	for i := 0; i < len(in); {
		b := in[i]
		i++
		switch {
		case b < 128:
			n := int(b) + 1
			if i+n > len(in) {
				return nil, errors.New("RunLengthDecode: truncated literal")
			}
			out = append(out, in[i:i+n]...)
			i += n
		case b > 128:
			n := 257 - int(b)
			if i >= len(in) {
				return nil, errors.New("RunLengthDecode: truncated run")
			}
			for k := 0; k < n; k++ {
				out = append(out, in[i])
			}
			i++
		default: // b == 128: EOD
			return out, nil
		}
	}
	return out, nil
}

func hexDigit(c byte) (int, bool) {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0'), true
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10, true
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10, true
	}
	return 0, false
}

// applyPredictor reverses the PNG / TIFF predictor wrapping applied to
// LZW/Flate data. See PDF 32000-1:2008 §7.4.4.4.
func applyPredictor(in []byte, p Params) ([]byte, error) {
	if p.Predictor <= 1 {
		return in, nil
	}
	colors := p.Colors
	if colors == 0 {
		colors = 1
	}
	bpc := p.BitsPerComponent
	if bpc == 0 {
		bpc = 8
	}
	columns := p.Columns
	if columns == 0 {
		columns = 1
	}
	bytesPerPixel := (colors*bpc + 7) / 8
	rowBytes := (columns*colors*bpc + 7) / 8

	if p.Predictor == 2 {
		// TIFF predictor 2: per-row horizontal differences. Not commonly
		// used in our domain but supported for completeness.
		if len(in)%rowBytes != 0 {
			return nil, fmt.Errorf("predictor 2: %d bytes not divisible by row %d", len(in), rowBytes)
		}
		out := make([]byte, len(in))
		for r := 0; r < len(in); r += rowBytes {
			row := in[r : r+rowBytes]
			dst := out[r : r+rowBytes]
			copy(dst, row)
			for c := bytesPerPixel; c < rowBytes; c++ {
				dst[c] = byte(int(row[c]) + int(dst[c-bytesPerPixel]))
			}
		}
		return out, nil
	}

	// PNG predictors: rowBytes data preceded by a 1-byte filter tag.
	stride := rowBytes + 1
	if len(in)%stride != 0 {
		return nil, fmt.Errorf("predictor PNG: %d bytes not divisible by row %d", len(in), stride)
	}
	rows := len(in) / stride
	out := make([]byte, rows*rowBytes)
	prev := make([]byte, rowBytes)
	cur := make([]byte, rowBytes)
	for r := 0; r < rows; r++ {
		tag := in[r*stride]
		row := in[r*stride+1 : (r+1)*stride]
		switch tag {
		case 0: // None
			copy(cur, row)
		case 1: // Sub
			for c := 0; c < rowBytes; c++ {
				var left byte
				if c >= bytesPerPixel {
					left = cur[c-bytesPerPixel]
				}
				cur[c] = row[c] + left
			}
		case 2: // Up
			for c := 0; c < rowBytes; c++ {
				cur[c] = row[c] + prev[c]
			}
		case 3: // Average
			for c := 0; c < rowBytes; c++ {
				var left byte
				if c >= bytesPerPixel {
					left = cur[c-bytesPerPixel]
				}
				cur[c] = row[c] + byte((int(left)+int(prev[c]))/2)
			}
		case 4: // Paeth
			for c := 0; c < rowBytes; c++ {
				var left, upLeft byte
				if c >= bytesPerPixel {
					left = cur[c-bytesPerPixel]
					upLeft = prev[c-bytesPerPixel]
				}
				cur[c] = row[c] + paeth(left, prev[c], upLeft)
			}
		default:
			return nil, fmt.Errorf("predictor PNG: unknown tag %d", tag)
		}
		copy(out[r*rowBytes:], cur)
		copy(prev, cur)
	}
	return out, nil
}

func paeth(a, b, c byte) byte {
	p := int(a) + int(b) - int(c)
	pa := abs(p - int(a))
	pb := abs(p - int(b))
	pc := abs(p - int(c))
	switch {
	case pa <= pb && pa <= pc:
		return a
	case pb <= pc:
		return b
	}
	return c
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
