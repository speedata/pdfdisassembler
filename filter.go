package pdfdisassembler

import (
	"fmt"

	"github.com/speedata/pdfdisassembler/internal/filter"
)

// decodeStream is the Stream.Content backend. It reads the raw bytes from
// the file, applies decryption if enabled, then runs the declared filter
// chain.
func (r *Reader) decodeStream(s *Stream) ([]byte, error) {
	if s.rawOffset < 0 || s.rawOffset+s.rawLength > int64(len(r.buf)) {
		return nil, fmt.Errorf("pdfdisassembler: stream %d %d R: bytes out of range", s.objNumber, s.objGeneration)
	}
	raw := r.buf[s.rawOffset : s.rawOffset+s.rawLength]
	return r.applyFilters(s, raw, true)
}

// applyFilters decrypts (if encrypted is true and an encryption context
// exists) and runs the filter chain declared on the stream dict.
func (r *Reader) applyFilters(s *Stream, raw []byte, encrypted bool) ([]byte, error) {
	data := raw
	if encrypted && r.encrypt != nil {
		// Cross-reference streams are themselves unencrypted; callers
		// must pass encrypted=false for those.
		dec, err := r.encrypt.decryptStream(data, s.objNumber, s.objGeneration, s.Dict)
		if err != nil {
			return nil, fmt.Errorf("pdfdisassembler: decrypt stream %d %d R: %w", s.objNumber, s.objGeneration, err)
		}
		data = dec
	}

	filters, params, err := r.streamFilterChain(s.Dict)
	if err != nil {
		return nil, fmt.Errorf("pdfdisassembler: stream %d %d R filter chain: %w", s.objNumber, s.objGeneration, err)
	}
	for i, name := range filters {
		// Skip image-only filters: return what we have and report.
		if filter.IsImageFilter(name) {
			return nil, fmt.Errorf("pdfdisassembler: stream %d %d R uses image-only filter %q (not decoded)", s.objNumber, s.objGeneration, name)
		}
		out, err := filter.Decode(name, data, params[i])
		if err != nil {
			return nil, fmt.Errorf("pdfdisassembler: stream %d %d R filter %q: %w", s.objNumber, s.objGeneration, name, err)
		}
		data = out
	}
	return data, nil
}

// streamFilterChain returns the ordered filter names and per-filter params
// for a stream dict. Both /Filter and /F (abbreviation) are accepted.
func (r *Reader) streamFilterChain(d *Dict) ([]string, []filter.Params, error) {
	v, ok := d.Get("Filter")
	if !ok {
		v, ok = d.Get("F")
	}
	if !ok {
		return nil, nil, nil
	}
	v, err := r.Resolve(v)
	if err != nil {
		return nil, nil, err
	}
	var names []string
	switch t := v.(type) {
	case Name:
		names = []string{string(t)}
	case Array:
		for _, e := range t {
			e, err := r.Resolve(e)
			if err != nil {
				return nil, nil, err
			}
			n, ok := e.(Name)
			if !ok {
				return nil, nil, fmt.Errorf("/Filter entry is %T, want Name", e)
			}
			names = append(names, string(n))
		}
	default:
		return nil, nil, fmt.Errorf("/Filter is %T", v)
	}

	pv, _ := d.Get("DecodeParms")
	if pv == nil {
		pv, _ = d.Get("DP")
	}
	if pv != nil {
		pv, err = r.Resolve(pv)
		if err != nil {
			return nil, nil, err
		}
	}
	params := make([]filter.Params, len(names))
	switch t := pv.(type) {
	case nil, Null:
		// nothing
	case *Dict:
		if len(names) >= 1 {
			params[0] = paramsFromDict(t)
		}
	case Array:
		for i, e := range t {
			if i >= len(params) {
				break
			}
			e, err := r.Resolve(e)
			if err != nil {
				return nil, nil, err
			}
			if d, ok := e.(*Dict); ok {
				params[i] = paramsFromDict(d)
			}
		}
	}
	return names, params, nil
}

func paramsFromDict(d *Dict) filter.Params {
	var p filter.Params
	if n, ok := d.Int("Predictor"); ok {
		p.Predictor = int(n)
	}
	if n, ok := d.Int("Columns"); ok {
		p.Columns = int(n)
	}
	if n, ok := d.Int("Colors"); ok {
		p.Colors = int(n)
	}
	if n, ok := d.Int("BitsPerComponent"); ok {
		p.BitsPerComponent = int(n)
	}
	if n, ok := d.Int("EarlyChange"); ok {
		p.EarlyChange = int(n)
	}
	return p
}
