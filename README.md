# pdfdisassembler

A focused, read-only PDF parser for Go. Built for tooling that **inspects**
PDFs — accessibility checkers, validators, debuggers — without dragging in
the writing, optimisation, signing and image-rendering machinery that
general-purpose PDF libraries carry.

## Status

Pre-1.0. The API may break between minor releases.

## Why

The Go PDF ecosystem has a real gap for read-only structural inspection.
Existing libraries are either too large (pdfcpu: ~50 kLOC, multi-MB WASM
overhead), licensed restrictively (unipdf: AGPL/commercial), CGo (go-fitz),
or too thin (rsc/pdf, ledongthuc/pdf). pdfdisassembler targets PDF 1.x and
2.0 reading in pure Go, WASM-friendly by construction.

## Scope

In scope: PDF 1.x and 2.0 reading, classical xref and xref streams,
indirect-object resolution, stream filters (FlateDecode, ASCII85, ASCIIHex,
LZW, RunLength), text-string decoding (PDFDocEncoding, UTF-16BE BOM, UTF-8
BOM), catalog + page tree, DocumentInfo, XMP metadata access, structure
tree traversal, `/Standard` security handler (V2, V4, V5), defensive
parsing.

Out of scope: writing PDFs, image filters (DCTDecode/JBIG2/JPX/CCITTFax),
image rendering, font internals, XFA, public-key encryption, signature
verification, content-stream graphics-state interpretation, LTV.

## Usage

```go
import "github.com/speedata/pdfdisassembler"

r, err := pdfdisassembler.OpenFile("doc.pdf")
if err != nil {
    return err
}
defer r.Close()

fmt.Println("PDF version:", r.Version())
info := r.DocumentInfo()
fmt.Println("Title:", info.Title)
```

## Testing

Snapshot tests live under `testdata/fixtures/<name>/`. Each fixture has an
`input.pdf` and a committed `golden.json`. `TestFixtures` opens every
fixture, runs `Dump`, and compares against the golden — a byte-stable
JSON snapshot of the object graph.

Adding a fixture:

1. Drop `input.pdf` into `testdata/fixtures/<name>/`
2. `go test -update -run TestFixtures/<name>` — generates `golden.json`
3. **Inspect the golden manually**: does it match what the PDF spec says
   should happen? The golden is *the expected behaviour*, not "what the
   parser currently does"
4. Commit the PDF, the golden, and an optional `README.md` explaining
   what the fixture proves

For synthetic fixtures, see `testdata/fixtures/generate.go`. Run it from
the repo root to (re)create the in-code fixture PDFs.

The same dump format is exposed as a CLI:

```
go install github.com/speedata/pdfdisassembler/cmd/pdfdump@latest
pdfdump doc.pdf > doc.json
diff <(pdfdump a.pdf) <(pdfdump b.pdf)
```

## License

MIT. See [LICENSE](LICENSE).
