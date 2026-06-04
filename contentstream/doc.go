// Package contentstream tokenises PDF content streams into a sequence
// of operations. Content streams are the postfix-notation graphics
// instructions that paint each page (text-showing operators, path
// operators, graphics-state ops, marked-content tags, …).
//
// The scanner is operand-aware: operands are collected up to each
// operator keyword and surfaced together as one Op. Inline images
// (BI/ID/EI) are folded into a single synthetic EI op so the binary
// image bytes between ID and EI do not derail tokenisation.
//
// The scanner does NOT interpret the operations: it does not track
// graphics state, does not render glyphs, does not resolve XObjects.
// Higher-level consumers (e.g. tagged-PDF validators) layer that logic
// on top.
//
// # Usage
//
//	for op, err := range contentstream.New(decoded).All() {
//	    if err != nil { ... }
//	    switch op.Operator {
//	    case "Tf":
//	        // op.Operands[0].Name is the font resource key
//	    case "BDC":
//	        // op.Operands[0].Name is the structure tag
//	        // op.Operands[1] is either a Name (ref into /Properties)
//	        // or a Dict (inline properties)
//	    }
//	}
//
// # Scope
//
// The scanner accepts the subset of PDF object syntax that can appear
// in content streams: numbers, names, strings (literal and hex),
// arrays, dictionaries, and operator keywords. Indirect references and
// stream objects do not occur in content streams and are not handled.
package contentstream
