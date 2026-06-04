package pdfdisassembler

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/speedata/pdfdisassembler/internal/lex"
)

// parser is a recursive descent parser over a lex.Lexer that emits direct
// PDF Objects. It does not chase indirect references — every Reference
// token becomes a Reference value.
type parser struct {
	lx    *lex.Lexer
	r     *Reader
	queue []lex.Token
}

func newParser(lx *lex.Lexer, r *Reader) *parser {
	return &parser{lx: lx, r: r}
}

func (p *parser) next() (lex.Token, error) {
	if len(p.queue) > 0 {
		t := p.queue[0]
		p.queue = p.queue[1:]
		return t, nil
	}
	return p.lx.Next()
}

func (p *parser) peek() (lex.Token, error) {
	if len(p.queue) > 0 {
		return p.queue[0], nil
	}
	t, err := p.lx.Next()
	if err != nil {
		return lex.Token{}, err
	}
	p.queue = append(p.queue, t)
	return t, nil
}

// peekN returns the n-th unread token (1-based). It reads from the lexer
// to fill the queue as needed.
func (p *parser) peekN(n int) (lex.Token, error) {
	for len(p.queue) < n {
		t, err := p.lx.Next()
		if err != nil {
			return lex.Token{}, err
		}
		p.queue = append(p.queue, t)
	}
	return p.queue[n-1], nil
}

// consume removes n tokens from the front of the queue (after a successful
// peekN). Caller must have ensured the queue has at least n entries.
func (p *parser) consume(n int) {
	p.queue = p.queue[n:]
}

// parseObject parses a single direct PDF object. It also recognises the
// "N G R" indirect-reference triplet, which requires two-token lookahead.
func (p *parser) parseObject() (Object, error) {
	tok, err := p.next()
	if err != nil {
		return nil, err
	}
	return p.parseObjectFrom(tok)
}

func (p *parser) parseObjectFrom(tok lex.Token) (Object, error) {
	switch tok.Kind {
	case lex.EOF:
		return nil, errors.New("pdfdisassembler/parse: unexpected EOF")
	case lex.Name:
		return Name(string(tok.Bytes)), nil
	case lex.LitString:
		// Copy: Bytes points into the source buffer of the literal-string
		// reader which is stable for our use, but ownership-wise we'd
		// rather not have callers alias the source.
		b := make(String, len(tok.Bytes))
		copy(b, tok.Bytes)
		return b, nil
	case lex.HexString:
		b := make(String, len(tok.Bytes))
		copy(b, tok.Bytes)
		return b, nil
	case lex.Integer:
		n, err := strconv.ParseInt(string(tok.Bytes), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("pdfdisassembler/parse: bad integer %q: %w", tok.Bytes, err)
		}
		// Look ahead two tokens for a Reference: "G R".
		t2, err := p.peekN(1)
		if err != nil || t2.Kind != lex.Integer {
			return Integer(n), nil
		}
		t3, err := p.peekN(2)
		if err != nil || t3.Kind != lex.Keyword || string(t3.Bytes) != "R" {
			return Integer(n), nil
		}
		g, err := strconv.Atoi(string(t2.Bytes))
		if err != nil {
			return nil, fmt.Errorf("pdfdisassembler/parse: bad generation %q: %w", t2.Bytes, err)
		}
		p.consume(2) // gen and 'R'
		return Reference{Number: int(n), Generation: g}, nil
	case lex.Real:
		f, err := strconv.ParseFloat(string(tok.Bytes), 64)
		if err != nil {
			return nil, fmt.Errorf("pdfdisassembler/parse: bad real %q: %w", tok.Bytes, err)
		}
		return Real(f), nil
	case lex.Keyword:
		switch string(tok.Bytes) {
		case "true":
			return Bool(true), nil
		case "false":
			return Bool(false), nil
		case "null":
			return Null{}, nil
		}
		return nil, fmt.Errorf("pdfdisassembler/parse: unexpected keyword %q at %d", tok.Bytes, tok.Offset)
	case lex.ArrayStart:
		return p.parseArray()
	case lex.DictStart:
		return p.parseDict()
	case lex.ArrayEnd, lex.DictEnd:
		return nil, fmt.Errorf("pdfdisassembler/parse: stray %s at %d", tok.Kind, tok.Offset)
	}
	return nil, fmt.Errorf("pdfdisassembler/parse: unhandled token %s at %d", tok.Kind, tok.Offset)
}

func (p *parser) parseArray() (Array, error) {
	var out Array
	for {
		t, err := p.peek()
		if err != nil {
			return nil, err
		}
		if t.Kind == lex.ArrayEnd {
			p.next()
			return out, nil
		}
		if t.Kind == lex.EOF {
			return nil, errors.New("pdfdisassembler/parse: unterminated array")
		}
		v, err := p.parseObject()
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
}

func (p *parser) parseDict() (*Dict, error) {
	d := newDict(p.r)
	for {
		t, err := p.peek()
		if err != nil {
			return nil, err
		}
		if t.Kind == lex.DictEnd {
			p.next()
			return d, nil
		}
		if t.Kind == lex.EOF {
			return nil, errors.New("pdfdisassembler/parse: unterminated dictionary")
		}
		if t.Kind != lex.Name {
			return nil, fmt.Errorf("pdfdisassembler/parse: dict key must be a name, got %s at %d", t.Kind, t.Offset)
		}
		p.next()
		key := string(t.Bytes)
		v, err := p.parseObject()
		if err != nil {
			return nil, err
		}
		d.set(key, v)
	}
}
