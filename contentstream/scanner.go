package contentstream

import (
	"errors"
	"fmt"
	"io"
	"iter"
	"strconv"

	"github.com/speedata/pdfdisassembler/internal/lex"
)

// Op is one content-stream operation: zero or more operands followed
// by an operator keyword (e.g. "Tf", "Tj", "BDC").
//
// For inline-image runs, Operator is "EI" and Image carries the raw
// bytes between ID and EI; the BI dictionary is in Operands[0] as a
// KindDict (or empty if BI carried no entries).
type Op struct {
	Operator string
	Operands []Operand
	Image    []byte
	// Offset is the byte position of the operator keyword in the
	// source slice. Useful for error messages and source ranges.
	Offset int64
}

// Scanner walks a decoded content stream and yields one Op at a time.
// It is not safe for concurrent use.
type Scanner struct {
	lx    *lex.Lexer
	stack []Operand
	depth int
	done  bool
}

// New returns a Scanner over the decoded content-stream bytes src.
// src is not copied. For pages whose /Contents is an array of streams,
// concatenate the decoded payloads with a single whitespace byte (per
// PDF 32000-1:2008 §7.8.2) before passing them in.
func New(src []byte) *Scanner {
	return &Scanner{lx: lex.New(src)}
}

// ErrUnexpectedEOF indicates that the scanner ran out of bytes mid-
// operation (e.g. inside a dictionary, or while looking for EI).
var ErrUnexpectedEOF = errors.New("pdfdisassembler/contentstream: unexpected EOF")

// maxNestDepth bounds array/dict nesting so a hostile content stream can't
// recurse the scanner into a stack overflow.
const maxNestDepth = 1000

// maxOperands caps operands accumulated per operation (and per array/dict) so
// a flood of operands can't pin large amounts of memory.
const maxOperands = 100000

// Next returns the next operation. At end of stream it returns io.EOF.
// Any other error indicates malformed input; the scanner is not safe
// to keep using after an error.
func (s *Scanner) Next() (Op, error) {
	if s.done {
		return Op{}, io.EOF
	}
	for {
		if len(s.stack) > maxOperands {
			return Op{}, fmt.Errorf("pdfdisassembler/contentstream: too many operands (> %d)", maxOperands)
		}
		tok, err := s.nextToken()
		if err != nil {
			return Op{}, err
		}
		switch tok.Kind {
		case lex.EOF:
			s.done = true
			if len(s.stack) != 0 {
				// Trailing operands without an operator — common in
				// the wild. Drop them silently.
				s.stack = s.stack[:0]
			}
			return Op{}, io.EOF
		case lex.Integer, lex.Real:
			n, _ := strconv.ParseFloat(string(tok.Bytes), 64)
			s.stack = append(s.stack, Operand{
				Kind:   KindNumber,
				Number: n,
				numStr: string(tok.Bytes),
			})
		case lex.Name:
			s.stack = append(s.stack, Operand{Kind: KindName, Name: string(tok.Bytes)})
		case lex.LitString, lex.HexString:
			s.stack = append(s.stack, Operand{Kind: KindString, Bytes: append([]byte(nil), tok.Bytes...)})
		case lex.ArrayStart:
			arr, err := s.readArray()
			if err != nil {
				return Op{}, err
			}
			s.stack = append(s.stack, Operand{Kind: KindArray, Array: arr})
		case lex.DictStart:
			d, err := s.readDict()
			if err != nil {
				return Op{}, err
			}
			s.stack = append(s.stack, Operand{Kind: KindDict, Dict: d})
		case lex.Keyword:
			kw := string(tok.Bytes)
			switch kw {
			case "true":
				s.stack = append(s.stack, Operand{Kind: KindBool, Bool: true})
				continue
			case "false":
				s.stack = append(s.stack, Operand{Kind: KindBool, Bool: false})
				continue
			case "null":
				s.stack = append(s.stack, Operand{Kind: KindNull})
				continue
			case "BI":
				img, err := s.readInlineImage()
				if err != nil {
					return Op{}, err
				}
				op := Op{Operator: "EI", Operands: s.takeStack(), Image: img, Offset: tok.Offset}
				return op, nil
			}
			op := Op{Operator: kw, Operands: s.takeStack(), Offset: tok.Offset}
			return op, nil
		default:
			return Op{}, fmt.Errorf("pdfdisassembler/contentstream: unexpected token %s at %d", tok.Kind, tok.Offset)
		}
	}
}

// All returns a range-over-func iterator that yields each Op until EOF
// or the first error. The error is delivered through the second loop
// variable as on the final iteration.
func (s *Scanner) All() iter.Seq2[Op, error] {
	return func(yield func(Op, error) bool) {
		for {
			op, err := s.Next()
			if err == io.EOF {
				return
			}
			if !yield(op, err) {
				return
			}
			if err != nil {
				return
			}
		}
	}
}

func (s *Scanner) takeStack() []Operand {
	out := s.stack
	s.stack = nil
	return out
}

func (s *Scanner) nextToken() (lex.Token, error) {
	return s.lx.Next()
}

func (s *Scanner) readArray() ([]Operand, error) {
	s.depth++
	defer func() { s.depth-- }()
	if s.depth > maxNestDepth {
		return nil, fmt.Errorf("pdfdisassembler/contentstream: nesting too deep (> %d)", maxNestDepth)
	}
	var out []Operand
	for {
		if len(out) > maxOperands {
			return nil, fmt.Errorf("pdfdisassembler/contentstream: array too large (> %d)", maxOperands)
		}
		tok, err := s.nextToken()
		if err != nil {
			return nil, err
		}
		switch tok.Kind {
		case lex.ArrayEnd:
			return out, nil
		case lex.EOF:
			return nil, ErrUnexpectedEOF
		case lex.Integer, lex.Real:
			n, _ := strconv.ParseFloat(string(tok.Bytes), 64)
			out = append(out, Operand{Kind: KindNumber, Number: n, numStr: string(tok.Bytes)})
		case lex.Name:
			out = append(out, Operand{Kind: KindName, Name: string(tok.Bytes)})
		case lex.LitString, lex.HexString:
			out = append(out, Operand{Kind: KindString, Bytes: append([]byte(nil), tok.Bytes...)})
		case lex.ArrayStart:
			nested, err := s.readArray()
			if err != nil {
				return nil, err
			}
			out = append(out, Operand{Kind: KindArray, Array: nested})
		case lex.DictStart:
			d, err := s.readDict()
			if err != nil {
				return nil, err
			}
			out = append(out, Operand{Kind: KindDict, Dict: d})
		case lex.Keyword:
			switch string(tok.Bytes) {
			case "true":
				out = append(out, Operand{Kind: KindBool, Bool: true})
			case "false":
				out = append(out, Operand{Kind: KindBool, Bool: false})
			case "null":
				out = append(out, Operand{Kind: KindNull})
			default:
				return nil, fmt.Errorf("pdfdisassembler/contentstream: unexpected keyword %q inside array at %d", tok.Bytes, tok.Offset)
			}
		default:
			return nil, fmt.Errorf("pdfdisassembler/contentstream: unexpected token %s inside array at %d", tok.Kind, tok.Offset)
		}
	}
}

func (s *Scanner) readDict() (Dict, error) {
	s.depth++
	defer func() { s.depth-- }()
	if s.depth > maxNestDepth {
		return nil, fmt.Errorf("pdfdisassembler/contentstream: nesting too deep (> %d)", maxNestDepth)
	}
	out := Dict{}
	for {
		if len(out) > maxOperands {
			return nil, fmt.Errorf("pdfdisassembler/contentstream: dict too large (> %d)", maxOperands)
		}
		tok, err := s.nextToken()
		if err != nil {
			return nil, err
		}
		if tok.Kind == lex.DictEnd {
			return out, nil
		}
		if tok.Kind == lex.EOF {
			return nil, ErrUnexpectedEOF
		}
		if tok.Kind != lex.Name {
			return nil, fmt.Errorf("pdfdisassembler/contentstream: expected name as dict key at %d, got %s", tok.Offset, tok.Kind)
		}
		key := string(tok.Bytes)
		val, err := s.readValue()
		if err != nil {
			return nil, err
		}
		out[key] = val
	}
}

func (s *Scanner) readValue() (Operand, error) {
	tok, err := s.nextToken()
	if err != nil {
		return Operand{}, err
	}
	switch tok.Kind {
	case lex.Integer, lex.Real:
		n, _ := strconv.ParseFloat(string(tok.Bytes), 64)
		return Operand{Kind: KindNumber, Number: n, numStr: string(tok.Bytes)}, nil
	case lex.Name:
		return Operand{Kind: KindName, Name: string(tok.Bytes)}, nil
	case lex.LitString, lex.HexString:
		return Operand{Kind: KindString, Bytes: append([]byte(nil), tok.Bytes...)}, nil
	case lex.ArrayStart:
		arr, err := s.readArray()
		if err != nil {
			return Operand{}, err
		}
		return Operand{Kind: KindArray, Array: arr}, nil
	case lex.DictStart:
		d, err := s.readDict()
		if err != nil {
			return Operand{}, err
		}
		return Operand{Kind: KindDict, Dict: d}, nil
	case lex.Keyword:
		switch string(tok.Bytes) {
		case "true":
			return Operand{Kind: KindBool, Bool: true}, nil
		case "false":
			return Operand{Kind: KindBool, Bool: false}, nil
		case "null":
			return Operand{Kind: KindNull}, nil
		}
		return Operand{}, fmt.Errorf("pdfdisassembler/contentstream: unexpected keyword %q where value expected at %d", tok.Bytes, tok.Offset)
	case lex.EOF:
		return Operand{}, ErrUnexpectedEOF
	default:
		return Operand{}, fmt.Errorf("pdfdisassembler/contentstream: unexpected token %s where value expected at %d", tok.Kind, tok.Offset)
	}
}

// readInlineImage handles BI…ID…EI. The BI token has already been
// consumed. We read a dictionary body until we see the "ID" keyword,
// then scan the raw source for the "EI" terminator and return the
// bytes in between as the image payload.
func (s *Scanner) readInlineImage() ([]byte, error) {
	// BI has no '<<' — entries follow directly until ID.
	dict := Dict{}
	for {
		tok, err := s.nextToken()
		if err != nil {
			return nil, err
		}
		if tok.Kind == lex.Keyword && string(tok.Bytes) == "ID" {
			break
		}
		if tok.Kind == lex.EOF {
			return nil, ErrUnexpectedEOF
		}
		if tok.Kind != lex.Name {
			return nil, fmt.Errorf("pdfdisassembler/contentstream: expected name inside BI block at %d, got %s", tok.Offset, tok.Kind)
		}
		key := string(tok.Bytes)
		val, err := s.readValue()
		if err != nil {
			return nil, err
		}
		dict[key] = val
	}
	// We stashed the dict in stack so it surfaces as Operand of EI.
	s.stack = append(s.stack, Operand{Kind: KindDict, Dict: dict})

	// Per PDF 32000-1:2008 §8.9.7, ID is followed by exactly one
	// whitespace byte, then the raw image data, then EI preceded by
	// whitespace. We approximate "preceded by whitespace" rather than
	// strictly enforcing exactly-one — producers vary.
	src := s.lx.Source()
	pos := s.lx.Pos()
	if pos < len(src) && (src[pos] == ' ' || src[pos] == '\t' || src[pos] == '\n' || src[pos] == '\r') {
		pos++
	}
	imgStart := pos
	// Scan for "EI" preceded by whitespace and followed by whitespace/EOF.
	for pos < len(src) {
		// Look for 'E' first.
		if src[pos] != 'E' {
			pos++
			continue
		}
		if pos+1 >= len(src) || src[pos+1] != 'I' {
			pos++
			continue
		}
		// Check leading boundary.
		if pos == 0 {
			pos++
			continue
		}
		if !lex.IsWhitespace(src[pos-1]) {
			pos++
			continue
		}
		// Check trailing boundary.
		if pos+2 == len(src) || lex.IsWhitespace(src[pos+2]) || lex.IsDelimiter(src[pos+2]) {
			imgEnd := pos - 1 // strip the whitespace separator
			if imgEnd < imgStart {
				imgEnd = imgStart // empty image: no data between ID and EI
			}
			s.lx.SetPos(pos + 2)
			return append([]byte(nil), src[imgStart:imgEnd]...), nil
		}
		pos++
	}
	return nil, ErrUnexpectedEOF
}
