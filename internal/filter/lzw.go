package filter

import "fmt"

// decodeLZW decodes a PDF LZW stream. Code widths grow from 9 to 12 bits;
// the early-change flag (default 1) shrinks the threshold at which each
// width step happens.
func decodeLZW(in []byte, p Params) ([]byte, error) {
	early := 1
	if p.NoEarlyChange {
		early = 0
	}
	const (
		clearCode = 256
		eodCode   = 257
	)
	br := bitReader{src: in}
	codeWidth := 9
	dict := make([][]byte, 258, 4096)
	for i := 0; i < 256; i++ {
		dict[i] = []byte{byte(i)}
	}

	var out []byte
	prev := -1

	resize := func() {
		codeWidth = 9
		dict = dict[:258]
	}

	for {
		code, ok := br.readBits(codeWidth)
		if !ok {
			break
		}
		switch {
		case code == clearCode:
			resize()
			prev = -1
			continue
		case code == eodCode:
			return out, nil
		}

		var entry []byte
		switch {
		case int(code) < len(dict):
			entry = dict[code]
		case int(code) == len(dict) && prev >= 0:
			pe := dict[prev]
			entry = make([]byte, len(pe)+1)
			copy(entry, pe)
			entry[len(pe)] = pe[0]
		default:
			return nil, fmt.Errorf("LZWDecode: invalid code %d at width %d", code, codeWidth)
		}
		out = append(out, entry...)
		if p.MaxOutput > 0 && int64(len(out)) > p.MaxOutput {
			return nil, fmt.Errorf("LZWDecode: decoded output exceeds limit of %d bytes (possible decompression bomb)", p.MaxOutput)
		}
		if prev >= 0 && len(dict) < 4096 {
			pe := dict[prev]
			ne := make([]byte, len(pe)+1)
			copy(ne, pe)
			ne[len(pe)] = entry[0]
			dict = append(dict, ne)
		}
		prev = int(code)

		// Grow width: the new code's index will be len(dict). We need to
		// switch when the next code may not fit.
		threshold := (1 << uint(codeWidth)) - early
		if len(dict) >= threshold && codeWidth < 12 {
			codeWidth++
		}
	}
	return out, nil
}

type bitReader struct {
	src     []byte
	bytePos int
	bitPos  uint // 0 = MSB unread
	buf     uint64
	have    uint // number of bits buffered
}

func (b *bitReader) readBits(n int) (uint32, bool) {
	for b.have < uint(n) {
		if b.bytePos >= len(b.src) {
			if b.have == 0 {
				return 0, false
			}
			// Pad with zeros to flush trailing partial word.
			b.buf <<= 8
			b.have += 8
			b.bytePos++
			continue
		}
		b.buf = (b.buf << 8) | uint64(b.src[b.bytePos])
		b.have += 8
		b.bytePos++
	}
	shift := b.have - uint(n)
	mask := (uint64(1) << uint(n)) - 1
	v := uint32((b.buf >> shift) & mask)
	b.have -= uint(n)
	b.buf &= (uint64(1) << b.have) - 1
	return v, true
}
