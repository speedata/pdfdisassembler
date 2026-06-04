// Package lex tokenises PDF input. It deals with the lexical layer of
// PDF objects — whitespace, comments, names, numbers, strings, arrays,
// dictionaries, the stream/endstream/obj/endobj/R/null/true/false keywords —
// but does not assemble higher-level structures. The parser layered above
// it turns token streams into Object trees.
package lex

import (
	"errors"
	"fmt"
)

// Kind identifies a token's lexical category.
type Kind int

const (
	// EOF marks end of input.
	EOF Kind = iota
	// Name is a PDF name without the leading slash.
	Name
	// Integer is a literal integer (no decimal point, optional sign).
	Integer
	// Real is a literal real number (has a decimal point or 'e' exponent —
	// PDF does not actually allow exponents but we accept them).
	Real
	// LitString is a parenthesised literal string with escapes already
	// resolved.
	LitString
	// HexString is an angle-bracketed hex string with hex pairs already
	// decoded to bytes.
	HexString
	// ArrayStart is the '[' token.
	ArrayStart
	// ArrayEnd is the ']' token.
	ArrayEnd
	// DictStart is the '<<' token.
	DictStart
	// DictEnd is the '>>' token.
	DictEnd
	// Keyword is any unquoted identifier: true, false, null, obj, endobj,
	// stream, endstream, R, xref, trailer, startxref, n, f.
	Keyword
)

func (k Kind) String() string {
	switch k {
	case EOF:
		return "EOF"
	case Name:
		return "Name"
	case Integer:
		return "Integer"
	case Real:
		return "Real"
	case LitString:
		return "LitString"
	case HexString:
		return "HexString"
	case ArrayStart:
		return "["
	case ArrayEnd:
		return "]"
	case DictStart:
		return "<<"
	case DictEnd:
		return ">>"
	case Keyword:
		return "Keyword"
	}
	return fmt.Sprintf("Kind(%d)", int(k))
}

// Token is a single lexical unit. Bytes carries the token payload; its
// meaning depends on Kind:
//   - Name, Keyword: ASCII name body, no leading slash
//   - Integer, Real: literal digits
//   - LitString, HexString: decoded bytes
//   - ArrayStart, ArrayEnd, DictStart, DictEnd, EOF: empty
type Token struct {
	Kind   Kind
	Bytes  []byte
	Offset int64 // byte offset in the input where this token started
}

// Lexer converts a byte slice into a stream of Tokens. It is not safe for
// concurrent use.
type Lexer struct {
	src []byte
	pos int
}

// New creates a Lexer over src. The src slice is not copied.
func New(src []byte) *Lexer {
	return &Lexer{src: src}
}

// Pos returns the current byte offset.
func (l *Lexer) Pos() int { return l.pos }

// SetPos rewinds or fast-forwards the lexer.
func (l *Lexer) SetPos(p int) { l.pos = p }

// Remaining returns the unread portion of the source.
func (l *Lexer) Remaining() []byte { return l.src[l.pos:] }

// Source returns the underlying source slice.
func (l *Lexer) Source() []byte { return l.src }

// ErrUnexpectedEOF indicates that the lexer ran out of bytes mid-token.
var ErrUnexpectedEOF = errors.New("pdfdisassembler/lex: unexpected EOF")

// IsWhitespace reports whether c is a PDF whitespace character (§7.2.2).
func IsWhitespace(c byte) bool {
	switch c {
	case 0, '\t', '\n', '\f', '\r', ' ':
		return true
	}
	return false
}

// IsDelimiter reports whether c is a PDF delimiter character (§7.2.2).
func IsDelimiter(c byte) bool {
	switch c {
	case '(', ')', '<', '>', '[', ']', '{', '}', '/', '%':
		return true
	}
	return false
}

// IsRegular reports whether c is a regular character (neither whitespace
// nor delimiter).
func IsRegular(c byte) bool { return !IsWhitespace(c) && !IsDelimiter(c) }

// SkipWhitespace advances over PDF whitespace and comments.
func (l *Lexer) SkipWhitespace() {
	for l.pos < len(l.src) {
		c := l.src[l.pos]
		if IsWhitespace(c) {
			l.pos++
			continue
		}
		if c == '%' {
			// Comment to end of line.
			for l.pos < len(l.src) && l.src[l.pos] != '\n' && l.src[l.pos] != '\r' {
				l.pos++
			}
			continue
		}
		return
	}
}

// Next returns the next token. At EOF it returns a Token with Kind=EOF.
func (l *Lexer) Next() (Token, error) {
	l.SkipWhitespace()
	if l.pos >= len(l.src) {
		return Token{Kind: EOF, Offset: int64(l.pos)}, nil
	}
	start := l.pos
	c := l.src[l.pos]

	switch {
	case c == '/':
		return l.readName(start)
	case c == '(':
		return l.readLiteralString(start)
	case c == '<':
		if l.pos+1 < len(l.src) && l.src[l.pos+1] == '<' {
			l.pos += 2
			return Token{Kind: DictStart, Offset: int64(start)}, nil
		}
		return l.readHexString(start)
	case c == '>':
		if l.pos+1 < len(l.src) && l.src[l.pos+1] == '>' {
			l.pos += 2
			return Token{Kind: DictEnd, Offset: int64(start)}, nil
		}
		return Token{}, fmt.Errorf("pdfdisassembler/lex: unexpected '>' at %d", l.pos)
	case c == '[':
		l.pos++
		return Token{Kind: ArrayStart, Offset: int64(start)}, nil
	case c == ']':
		l.pos++
		return Token{Kind: ArrayEnd, Offset: int64(start)}, nil
	case c == '+' || c == '-' || c == '.' || (c >= '0' && c <= '9'):
		return l.readNumber(start)
	default:
		return l.readKeyword(start)
	}
}

func (l *Lexer) readName(start int) (Token, error) {
	l.pos++ // skip '/'
	nameStart := l.pos
	var buf []byte
	for l.pos < len(l.src) {
		c := l.src[l.pos]
		if IsWhitespace(c) || IsDelimiter(c) {
			break
		}
		if c == '#' {
			if buf == nil {
				buf = append(buf, l.src[nameStart:l.pos]...)
			}
			if l.pos+2 >= len(l.src) {
				return Token{}, ErrUnexpectedEOF
			}
			hi, ok1 := hexDigit(l.src[l.pos+1])
			lo, ok2 := hexDigit(l.src[l.pos+2])
			if !ok1 || !ok2 {
				return Token{}, fmt.Errorf("pdfdisassembler/lex: invalid #XX escape in name at %d", l.pos)
			}
			buf = append(buf, byte(hi<<4|lo))
			l.pos += 3
			continue
		}
		if buf != nil {
			buf = append(buf, c)
		}
		l.pos++
	}
	if buf == nil {
		buf = l.src[nameStart:l.pos]
	}
	return Token{Kind: Name, Bytes: buf, Offset: int64(start)}, nil
}

func (l *Lexer) readLiteralString(start int) (Token, error) {
	l.pos++ // skip '('
	depth := 1
	var buf []byte
	for l.pos < len(l.src) {
		c := l.src[l.pos]
		switch c {
		case '(':
			depth++
			buf = append(buf, c)
			l.pos++
		case ')':
			depth--
			if depth == 0 {
				l.pos++
				return Token{Kind: LitString, Bytes: buf, Offset: int64(start)}, nil
			}
			buf = append(buf, c)
			l.pos++
		case '\\':
			if l.pos+1 >= len(l.src) {
				return Token{}, ErrUnexpectedEOF
			}
			next := l.src[l.pos+1]
			switch next {
			case 'n':
				buf = append(buf, '\n')
				l.pos += 2
			case 'r':
				buf = append(buf, '\r')
				l.pos += 2
			case 't':
				buf = append(buf, '\t')
				l.pos += 2
			case 'b':
				buf = append(buf, '\b')
				l.pos += 2
			case 'f':
				buf = append(buf, '\f')
				l.pos += 2
			case '(':
				buf = append(buf, '(')
				l.pos += 2
			case ')':
				buf = append(buf, ')')
				l.pos += 2
			case '\\':
				buf = append(buf, '\\')
				l.pos += 2
			case '\n':
				// line continuation
				l.pos += 2
			case '\r':
				l.pos += 2
				if l.pos < len(l.src) && l.src[l.pos] == '\n' {
					l.pos++
				}
			case '0', '1', '2', '3', '4', '5', '6', '7':
				// Octal: up to 3 digits.
				v := 0
				n := 0
				p := l.pos + 1
				for n < 3 && p < len(l.src) {
					d := l.src[p]
					if d < '0' || d > '7' {
						break
					}
					v = v*8 + int(d-'0')
					p++
					n++
				}
				buf = append(buf, byte(v&0xFF))
				l.pos = p
			default:
				// Unknown escape: drop the backslash, keep next byte.
				buf = append(buf, next)
				l.pos += 2
			}
		case '\r':
			// CR or CRLF inside literal becomes LF (per spec §7.3.4.2).
			buf = append(buf, '\n')
			l.pos++
			if l.pos < len(l.src) && l.src[l.pos] == '\n' {
				l.pos++
			}
		default:
			buf = append(buf, c)
			l.pos++
		}
	}
	return Token{}, ErrUnexpectedEOF
}

func (l *Lexer) readHexString(start int) (Token, error) {
	l.pos++ // skip '<'
	var buf []byte
	var hi int
	have := false
	for l.pos < len(l.src) {
		c := l.src[l.pos]
		if c == '>' {
			if have {
				buf = append(buf, byte(hi<<4))
			}
			l.pos++
			return Token{Kind: HexString, Bytes: buf, Offset: int64(start)}, nil
		}
		if IsWhitespace(c) {
			l.pos++
			continue
		}
		d, ok := hexDigit(c)
		if !ok {
			return Token{}, fmt.Errorf("pdfdisassembler/lex: invalid hex digit %q at %d", c, l.pos)
		}
		if have {
			buf = append(buf, byte(hi<<4|d))
			have = false
		} else {
			hi = d
			have = true
		}
		l.pos++
	}
	return Token{}, ErrUnexpectedEOF
}

func (l *Lexer) readNumber(start int) (Token, error) {
	isReal := false
	p := l.pos
	if p < len(l.src) && (l.src[p] == '+' || l.src[p] == '-') {
		p++
	}
	for p < len(l.src) {
		c := l.src[p]
		if c == '.' {
			isReal = true
			p++
			continue
		}
		if c >= '0' && c <= '9' {
			p++
			continue
		}
		break
	}
	tok := Token{Bytes: l.src[l.pos:p], Offset: int64(start)}
	if isReal {
		tok.Kind = Real
	} else {
		tok.Kind = Integer
	}
	l.pos = p
	return tok, nil
}

func (l *Lexer) readKeyword(start int) (Token, error) {
	p := l.pos
	for p < len(l.src) && IsRegular(l.src[p]) {
		p++
	}
	if p == l.pos {
		return Token{}, fmt.Errorf("pdfdisassembler/lex: stuck at byte 0x%02x at %d", l.src[l.pos], l.pos)
	}
	tok := Token{Kind: Keyword, Bytes: l.src[l.pos:p], Offset: int64(start)}
	l.pos = p
	return tok, nil
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

// ReadStreamData consumes raw stream bytes of the given length, starting
// at the current position. It honours the spec's EOL handling: a single
// LF or CRLF *immediately* after the "stream" keyword is part of the
// keyword line, not the stream content. Callers should call this after
// the "stream" keyword token has been consumed.
func (l *Lexer) ReadStreamData(length int) ([]byte, error) {
	// Skip optional CR LF or single LF following "stream".
	if l.pos < len(l.src) && l.src[l.pos] == '\r' {
		l.pos++
	}
	if l.pos < len(l.src) && l.src[l.pos] == '\n' {
		l.pos++
	}
	if l.pos+length > len(l.src) {
		return nil, ErrUnexpectedEOF
	}
	out := l.src[l.pos : l.pos+length]
	l.pos += length
	return out, nil
}
