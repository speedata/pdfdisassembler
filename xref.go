package pdfdisassembler

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"

	"github.com/speedata/pdfdisassembler/internal/lex"
)

func trimLeftSpace(s string) string {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != ' ' && c != '\t' {
			return s[i:]
		}
	}
	return ""
}

// parseXref locates the last startxref offset, then parses the cross-
// reference table (classical, xref-stream, or hybrid) and walks /Prev
// chains. If the declared xref location is broken, parseXref falls back
// to xref recovery (scanning for "obj" markers).
func (r *Reader) parseXref() error {
	off, err := r.findStartXref()
	if err != nil {
		if recErr := r.recoverXref(); recErr != nil {
			return fmt.Errorf("pdfdisassembler: startxref missing and recovery failed: %v (recovery: %w)", err, recErr)
		}
		return nil
	}

	visited := map[int64]bool{}
	cur := off
	for {
		if visited[cur] {
			return fmt.Errorf("pdfdisassembler: xref loop at offset %d", cur)
		}
		visited[cur] = true

		prev, err := r.readXrefAt(cur)
		if err != nil {
			// Recover if the first attempt was wrong; xref chains
			// otherwise abort here.
			if len(visited) == 1 {
				if recErr := r.recoverXref(); recErr != nil {
					return fmt.Errorf("pdfdisassembler: xref at %d failed: %v (recovery: %w)", cur, err, recErr)
				}
				return nil
			}
			return err
		}
		if prev == 0 {
			break
		}
		cur = prev
	}

	if r.trailer == nil {
		return errors.New("pdfdisassembler: no trailer found")
	}
	return nil
}

// findStartXref scans the last 1024 bytes of the file for the "startxref"
// marker and returns the offset that follows it.
func (r *Reader) findStartXref() (int64, error) {
	const tail = 1024
	start := len(r.buf) - tail
	if start < 0 {
		start = 0
	}
	idx := bytes.LastIndex(r.buf[start:], []byte("startxref"))
	if idx < 0 {
		return 0, errors.New("startxref not found")
	}
	idx += start + len("startxref")
	// Skip whitespace, then read decimal.
	for idx < len(r.buf) && (r.buf[idx] == ' ' || r.buf[idx] == '\t' ||
		r.buf[idx] == '\r' || r.buf[idx] == '\n') {
		idx++
	}
	end := idx
	for end < len(r.buf) && r.buf[end] >= '0' && r.buf[end] <= '9' {
		end++
	}
	if end == idx {
		return 0, errors.New("startxref offset missing")
	}
	off, err := strconv.ParseInt(string(r.buf[idx:end]), 10, 64)
	if err != nil {
		return 0, err
	}
	return off, nil
}

// readXrefAt parses an xref section starting at offset. Returns the /Prev
// offset (0 if no previous section).
func (r *Reader) readXrefAt(offset int64) (int64, error) {
	if offset < 0 || offset >= int64(len(r.buf)) {
		return 0, fmt.Errorf("xref offset %d out of range", offset)
	}
	// Classical sections start with the keyword "xref".
	rest := r.buf[offset:]
	// Skip whitespace.
	i := 0
	for i < len(rest) && lex.IsWhitespace(rest[i]) {
		i++
	}
	if i+4 <= len(rest) && string(rest[i:i+4]) == "xref" {
		return r.readClassicalXrefAt(offset + int64(i))
	}
	return r.readXrefStreamAt(offset)
}

// readClassicalXrefAt parses a "xref" subsection table and the following
// trailer dictionary. Returns the /Prev offset (0 if none).
func (r *Reader) readClassicalXrefAt(offset int64) (int64, error) {
	pos := int(offset)
	// Skip the "xref" keyword and following EOL.
	pos += 4
	pos = skipEOL(r.buf, pos)

	for {
		// Each subsection: "first count" then count entries of 20 bytes
		// each. The subsection list ends at "trailer".
		lineEnd := indexEOL(r.buf, pos)
		if lineEnd < 0 {
			return 0, errors.New("classical xref: unterminated subsection header")
		}
		line := string(bytes.TrimSpace(r.buf[pos:lineEnd]))
		if line == "trailer" {
			// trailer follows.
			pos = skipEOL(r.buf, pos+len("trailer"))
			break
		}
		// Some producers put trailer on its own line further down.
		if line == "" {
			pos = skipEOL(r.buf, lineEnd)
			continue
		}
		parts := bytes.Fields(r.buf[pos:lineEnd])
		if len(parts) != 2 {
			return 0, fmt.Errorf("classical xref: bad subsection header %q", line)
		}
		first, err1 := strconv.Atoi(string(parts[0]))
		count, err2 := strconv.Atoi(string(parts[1]))
		if err1 != nil || err2 != nil {
			return 0, fmt.Errorf("classical xref: bad subsection header %q", line)
		}
		pos = skipEOL(r.buf, lineEnd)

		for i := 0; i < count; i++ {
			if pos+20 > len(r.buf) {
				return 0, errors.New("classical xref: truncated entry")
			}
			entry := r.buf[pos : pos+20]
			pos += 20
			// Format: nnnnnnnnnn ggggg t EOL
			if len(entry) < 18 {
				return 0, errors.New("classical xref: short entry")
			}
			offStr := string(entry[0:10])
			genStr := string(entry[11:16])
			flag := entry[17]
			off, _ := strconv.ParseInt(trimLeftSpace(offStr), 10, 64)
			gen, _ := strconv.Atoi(trimLeftSpace(genStr))
			ref := Reference{Number: first + i, Generation: gen}
			if flag == 'n' {
				if _, exists := r.xref[ref]; !exists {
					r.xref[ref] = xrefEntry{kind: 1, offset: off, generation: gen}
				}
			}
			// 'f' entries are free; ignore.
		}
	}

	// Parse trailer dictionary.
	lx := lex.New(r.buf)
	lx.SetPos(pos)
	p := newParser(lx, r)
	tok, err := p.next()
	if err != nil {
		return 0, err
	}
	if tok.Kind != lex.DictStart {
		return 0, fmt.Errorf("classical xref: trailer dict missing, got %s", tok.Kind)
	}
	trailer, err := p.parseDict()
	if err != nil {
		return 0, fmt.Errorf("classical xref: trailer parse: %w", err)
	}
	if r.trailer == nil {
		r.trailer = trailer
	} else {
		// Older trailers fill in missing keys only.
		for k, v := range trailer.Iter() {
			if !r.trailer.Has(k) {
				r.trailer.set(k, v)
			}
		}
	}

	// Hybrid: trailer may reference an XRefStm.
	if v, ok := trailer.Get("XRefStm"); ok {
		if off, ok := v.(Integer); ok {
			if _, err := r.readXrefStreamAt(int64(off)); err != nil {
				// non-fatal: log via error wrap
				return 0, fmt.Errorf("hybrid XRefStm: %w", err)
			}
		}
	}

	if v, ok := trailer.Get("Prev"); ok {
		if n, ok := v.(Integer); ok {
			return int64(n), nil
		}
	}
	return 0, nil
}

// readXrefStreamAt parses an xref stream at the given offset. Returns the
// /Prev offset (0 if none).
func (r *Reader) readXrefStreamAt(offset int64) (int64, error) {
	if offset < 0 || offset >= int64(len(r.buf)) {
		return 0, fmt.Errorf("xref stream offset %d out of range", offset)
	}
	lx := lex.New(r.buf)
	lx.SetPos(int(offset))
	p := newParser(lx, r)

	// "N G obj"
	t1, err := p.next()
	if err != nil {
		return 0, err
	}
	t2, err := p.next()
	if err != nil {
		return 0, err
	}
	t3, err := p.next()
	if err != nil {
		return 0, err
	}
	if t1.Kind != lex.Integer || t2.Kind != lex.Integer ||
		t3.Kind != lex.Keyword || string(t3.Bytes) != "obj" {
		return 0, fmt.Errorf("xref stream: bad indirect header at %d", offset)
	}
	objNum, _ := strconv.Atoi(string(t1.Bytes))
	objGen, _ := strconv.Atoi(string(t2.Bytes))

	body, err := p.parseObject()
	if err != nil {
		return 0, fmt.Errorf("xref stream: dict parse: %w", err)
	}
	d, ok := body.(*Dict)
	if !ok {
		return 0, fmt.Errorf("xref stream: body is %T, want dict", body)
	}
	tok, err := p.peek()
	if err != nil {
		return 0, err
	}
	if tok.Kind != lex.Keyword || string(tok.Bytes) != "stream" {
		return 0, fmt.Errorf("xref stream: missing stream keyword")
	}
	p.next()
	length, err := r.streamLength(d)
	if err != nil {
		return 0, err
	}
	raw, err := lx.ReadStreamData(int(length))
	if err != nil {
		return 0, err
	}

	// The xref stream itself is unencrypted per spec (encrypt context not
	// yet initialised at this point either way).
	stream := &Stream{
		Dict:          d,
		reader:        r,
		rawOffset:     int64(lx.Pos() - len(raw)),
		rawLength:     int64(len(raw)),
		objNumber:     objNum,
		objGeneration: objGen,
	}
	decoded, err := r.applyFilters(stream, raw, false)
	if err != nil {
		return 0, fmt.Errorf("xref stream: decode: %w", err)
	}

	// Store the trailer (xref-stream dict doubles as trailer).
	if r.trailer == nil {
		r.trailer = d
	} else {
		for k, v := range d.Iter() {
			if !r.trailer.Has(k) {
				r.trailer.set(k, v)
			}
		}
	}

	// Read W field widths.
	wArr, ok := d.Array("W")
	if !ok || len(wArr) < 3 {
		return 0, errors.New("xref stream: /W missing or too short")
	}
	w := make([]int, len(wArr))
	for i, v := range wArr {
		n, ok := v.(Integer)
		if !ok {
			return 0, fmt.Errorf("xref stream: /W[%d] not integer", i)
		}
		w[i] = int(n)
	}
	rowSize := 0
	for _, n := range w {
		rowSize += n
	}
	if rowSize <= 0 {
		return 0, errors.New("xref stream: zero row size")
	}

	// /Index is [first count first count …]; defaults to [0 Size].
	var index []int
	if arr, ok := d.Array("Index"); ok {
		for _, v := range arr {
			n, ok := v.(Integer)
			if !ok {
				return 0, errors.New("xref stream: /Index entry not integer")
			}
			index = append(index, int(n))
		}
	} else {
		size, ok := d.Int("Size")
		if !ok {
			return 0, errors.New("xref stream: /Size missing")
		}
		index = []int{0, int(size)}
	}

	rowIdx := 0
	for i := 0; i+1 < len(index); i += 2 {
		first := index[i]
		count := index[i+1]
		for j := 0; j < count; j++ {
			start := rowIdx * rowSize
			if start+rowSize > len(decoded) {
				return 0, errors.New("xref stream: data truncated")
			}
			row := decoded[start : start+rowSize]
			rowIdx++

			// Default type when W[0]==0 is 1.
			var t uint64 = 1
			off := 0
			if w[0] > 0 {
				t = readBigEndian(row[0:w[0]])
				off = w[0]
			}
			f1 := readBigEndian(row[off : off+w[1]])
			off += w[1]
			f2 := readBigEndian(row[off : off+w[2]])

			ref := Reference{Number: first + j, Generation: int(f2)}
			switch t {
			case 0:
				// free entry; ignore
			case 1:
				ref.Generation = int(f2)
				if _, exists := r.xref[ref]; !exists {
					r.xref[ref] = xrefEntry{kind: 1, offset: int64(f1), generation: int(f2)}
				}
			case 2:
				ref.Generation = 0
				if _, exists := r.xref[ref]; !exists {
					r.xref[ref] = xrefEntry{
						kind:      2,
						objStmNum: int(f1),
						objStmIdx: int(f2),
					}
				}
			default:
				// Unknown type per spec: skip.
			}
		}
	}

	if v, ok := d.Get("Prev"); ok {
		if n, ok := v.(Integer); ok {
			return int64(n), nil
		}
	}
	return 0, nil
}

// readCompressedObject extracts an object from an object stream (ObjStm).
func (r *Reader) readCompressedObject(objStmNum, idx int, expect Reference) (Object, error) {
	streamRef := Reference{Number: objStmNum, Generation: 0}
	v, err := r.Resolve(streamRef)
	if err != nil {
		return nil, fmt.Errorf("ObjStm %d %d R: %w", objStmNum, 0, err)
	}
	stm, ok := v.(*Stream)
	if !ok {
		return nil, fmt.Errorf("ObjStm %d resolved to %T, want stream", objStmNum, v)
	}
	d := stm.Dict
	if t, ok := d.Name("Type"); !ok || t != "ObjStm" {
		return nil, fmt.Errorf("ObjStm %d not of /Type ObjStm", objStmNum)
	}
	n, ok := d.Int("N")
	if !ok {
		return nil, fmt.Errorf("ObjStm %d missing /N", objStmNum)
	}
	first, ok := d.Int("First")
	if !ok {
		return nil, fmt.Errorf("ObjStm %d missing /First", objStmNum)
	}
	content, err := stm.Content()
	if err != nil {
		return nil, err
	}

	// Read N pairs of (objNum, offset) from the header section.
	header := content[:first]
	lx := lex.New(header)
	type pair struct {
		num    int
		offset int
	}
	pairs := make([]pair, 0, n)
	for i := int64(0); i < n; i++ {
		t1, err := lx.Next()
		if err != nil || t1.Kind != lex.Integer {
			return nil, fmt.Errorf("ObjStm %d header: bad object number", objStmNum)
		}
		t2, err := lx.Next()
		if err != nil || t2.Kind != lex.Integer {
			return nil, fmt.Errorf("ObjStm %d header: bad offset", objStmNum)
		}
		num, _ := strconv.Atoi(string(t1.Bytes))
		off, _ := strconv.Atoi(string(t2.Bytes))
		pairs = append(pairs, pair{num: num, offset: off})
	}
	if idx < 0 || idx >= len(pairs) {
		return nil, fmt.Errorf("ObjStm %d: index %d out of range (%d entries)", objStmNum, idx, len(pairs))
	}
	if pairs[idx].num != expect.Number {
		return nil, fmt.Errorf("ObjStm %d: index %d declares object %d, expected %d",
			objStmNum, idx, pairs[idx].num, expect.Number)
	}
	objStart := int(first) + pairs[idx].offset
	if objStart < 0 || objStart >= len(content) {
		return nil, fmt.Errorf("ObjStm %d: object %d offset %d out of range", objStmNum, expect.Number, objStart)
	}
	bodyLex := lex.New(content)
	bodyLex.SetPos(objStart)
	bp := newParser(bodyLex, r)
	return bp.parseObject()
}

// recoverXref scans the file for "obj" tokens and rebuilds the xref table.
// Called when the declared xref location is broken.
func (r *Reader) recoverXref() error {
	// Find every "N G obj" occurrence in the file.
	for i := 0; i < len(r.buf); i++ {
		if i+3 > len(r.buf) || string(r.buf[i:i+3]) != "obj" {
			continue
		}
		// Must be preceded by whitespace, two integers, whitespace.
		j := i - 1
		for j >= 0 && lex.IsWhitespace(r.buf[j]) {
			j--
		}
		genEnd := j + 1
		for j >= 0 && r.buf[j] >= '0' && r.buf[j] <= '9' {
			j--
		}
		genStart := j + 1
		if genStart == genEnd {
			continue
		}
		for j >= 0 && lex.IsWhitespace(r.buf[j]) {
			j--
		}
		numEnd := j + 1
		for j >= 0 && r.buf[j] >= '0' && r.buf[j] <= '9' {
			j--
		}
		numStart := j + 1
		if numStart == numEnd {
			continue
		}
		num, err1 := strconv.Atoi(string(r.buf[numStart:numEnd]))
		gen, err2 := strconv.Atoi(string(r.buf[genStart:genEnd]))
		if err1 != nil || err2 != nil {
			continue
		}
		// "obj" must be followed by whitespace.
		if i+3 < len(r.buf) && !lex.IsWhitespace(r.buf[i+3]) {
			continue
		}
		ref := Reference{Number: num, Generation: gen}
		if _, exists := r.xref[ref]; !exists {
			r.xref[ref] = xrefEntry{kind: 1, offset: int64(numStart), generation: gen}
		}
		i += 3
	}

	// Also try to find the trailer dict.
	if r.trailer == nil {
		idx := bytes.LastIndex(r.buf, []byte("trailer"))
		if idx >= 0 {
			lx := lex.New(r.buf)
			lx.SetPos(idx + len("trailer"))
			p := newParser(lx, r)
			tok, err := p.next()
			if err == nil && tok.Kind == lex.DictStart {
				if d, err := p.parseDict(); err == nil {
					r.trailer = d
				}
			}
		}
	}
	// If still no trailer but we have a /Root somewhere, try harder by
	// finding a dict with /Root in the recovered objects.
	if r.trailer == nil {
		for ref := range r.xref {
			obj, err := r.Resolve(ref)
			if err != nil {
				continue
			}
			d, ok := obj.(*Dict)
			if !ok {
				continue
			}
			if t, ok := d.Name("Type"); ok && t == "Catalog" {
				t := newDict(r)
				t.set("Root", ref)
				r.trailer = t
				break
			}
		}
	}
	if r.trailer == nil {
		return errors.New("xref recovery: no trailer found")
	}
	return nil
}

// readBigEndian reads a big-endian unsigned integer of the given byte
// width.
func readBigEndian(b []byte) uint64 {
	var v uint64
	for _, c := range b {
		v = (v << 8) | uint64(c)
	}
	return v
}

func skipEOL(buf []byte, pos int) int {
	for pos < len(buf) {
		c := buf[pos]
		if c == '\r' {
			pos++
			if pos < len(buf) && buf[pos] == '\n' {
				pos++
			}
			return pos
		}
		if c == '\n' {
			return pos + 1
		}
		if c == ' ' || c == '\t' {
			pos++
			continue
		}
		return pos
	}
	return pos
}

func indexEOL(buf []byte, pos int) int {
	for i := pos; i < len(buf); i++ {
		if buf[i] == '\r' || buf[i] == '\n' {
			return i
		}
	}
	return -1
}
