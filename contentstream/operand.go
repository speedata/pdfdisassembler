package contentstream

import "strconv"

// Kind identifies the type of an operand value.
type Kind int

const (
	// KindUnknown is the zero value; not produced by the scanner.
	KindUnknown Kind = iota
	// KindNumber covers both PDF integers and reals. Use Operand.Int()
	// to recover an int64 when the producer wrote an integer literal.
	KindNumber
	// KindName is a PDF name without the leading slash.
	KindName
	// KindString is a literal or hex string. The raw decoded bytes are
	// in Operand.Bytes; the scanner does not apply text-string decoding
	// (UTF-16BE BOM, PDFDocEncoding, …) because content-stream strings
	// are text shown to the reader and their semantic encoding depends
	// on the active font, not on the PDF text-string convention.
	KindString
	// KindArray holds operands of a PDF array, in source order. The
	// most common occurrence is the operand of TJ: a mix of strings
	// and number adjustments.
	KindArray
	// KindDict holds the entries of an inline dictionary. The most
	// common occurrence is the property dictionary that follows BDC.
	KindDict
	// KindBool is rare in content streams but appears in BDC property
	// dictionaries occasionally.
	KindBool
	// KindNull is rare in content streams but appears in BDC property
	// dictionaries occasionally.
	KindNull
)

// Operand is a single value pushed onto the operand stack before an
// operator keyword. It is a tagged union: which field is meaningful
// depends on Kind.
type Operand struct {
	Kind Kind
	// Number carries the parsed numeric value when Kind == KindNumber.
	// numStr preserves the original literal so Int() can decide whether
	// the producer wrote an integer.
	Number float64
	numStr string
	// Name is the name body (no leading slash) when Kind == KindName.
	Name string
	// Bytes is the decoded string payload when Kind == KindString.
	Bytes []byte
	// Array is the element list when Kind == KindArray.
	Array []Operand
	// Dict is the entry map when Kind == KindDict. Iteration order is
	// not preserved; use Dict.Keys / parse separately if order matters.
	Dict Dict
	// Bool is the boolean value when Kind == KindBool.
	Bool bool
}

// Int reports the operand as an int64 if the producer wrote an integer
// literal (no decimal point, no exponent). The ok flag is false for
// real-number literals and for non-number operands.
func (o Operand) Int() (int64, bool) {
	if o.Kind != KindNumber {
		return 0, false
	}
	for _, c := range o.numStr {
		if c == '.' || c == 'e' || c == 'E' {
			return 0, false
		}
	}
	v, err := strconv.ParseInt(o.numStr, 10, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// Dict is a small key→Operand map for inline content-stream
// dictionaries. Nested dictionaries are supported.
type Dict map[string]Operand
