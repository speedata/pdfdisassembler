// Package pdfdisassembler is a focused, read-only PDF parser for Go.
//
// It targets tooling that *inspects* PDFs — accessibility checkers,
// validators, debuggers — without dragging in the writing, optimisation,
// signing and image-rendering machinery that general-purpose PDF libraries
// carry.
//
// # Scope
//
// In scope: PDF 1.x and 2.0 reading, classical xref and xref streams,
// indirect-object resolution, stream filters (FlateDecode, ASCII85,
// ASCIIHex, LZW, RunLength), text-string decoding (PDFDocEncoding,
// UTF-16BE BOM, UTF-8 BOM), catalog + page tree, DocumentInfo, XMP
// metadata access, structure-tree traversal, the /Standard security
// handler (V2, V4, V5), defensive xref recovery.
//
// Out of scope: writing PDFs, image filters (DCTDecode/JBIG2/JPX/CCITTFax),
// image rendering, font internals, XFA, public-key encryption, signature
// verification, content-stream graphics-state interpretation, LTV.
//
// # Usage
//
//	r, err := pdfdisassembler.OpenFile("doc.pdf")
//	if err != nil {
//	    return err
//	}
//	defer r.Close()
//
//	fmt.Println("PDF version:", r.Version())
//	info := r.DocumentInfo()
//	fmt.Println("Title:", info.Title)
//
//	for entry := range r.Objects() {
//	    // inspect every live indirect object
//	    _ = entry
//	}
//
// # API stability
//
// Pre-1.0. The API may break between minor releases but never within a
// patch release.
package pdfdisassembler
