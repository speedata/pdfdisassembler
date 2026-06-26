# pdfdisassembler

[![Test](https://github.com/speedata/pdfdisassembler/actions/workflows/test.yml/badge.svg)](https://github.com/speedata/pdfdisassembler/actions/workflows/test.yml)
[![Coverage](https://github.com/speedata/pdfdisassembler/raw/badges/.badges/main/coverage.svg)](https://github.com/speedata/pdfdisassembler/actions/workflows/test.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/speedata/pdfdisassembler.svg)](https://pkg.go.dev/github.com/speedata/pdfdisassembler)


A focused, read-only PDF parser for Go. Built for tooling that **inspects**
PDFs — accessibility checkers, validators, debuggers — without dragging in
the writing, optimisation, signing and image-rendering machinery that
general-purpose PDF libraries carry.

Full API documentation: <https://pkg.go.dev/github.com/speedata/pdfdisassembler>

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
BOM), catalog + page-tree navigation (page boxes, rotation, resources and
content streams, with inherited attributes resolved along the `/Parent`
chain), DocumentInfo, XMP metadata access, structure tree traversal,
`/Standard` security handler (V2, V4, V5), defensive parsing.

Out of scope: writing PDFs, image filters (DCTDecode/JBIG2/JPX/CCITTFax),
image rendering, font internals, XFA, public-key encryption, signature
verification, content-stream graphics-state interpretation, LTV.

## Usage

Open a file and read top-level metadata:

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

Walk every live indirect object and decode any streams that carry one of
the supported filters:

```go
r, err := pdfdisassembler.OpenFile("doc.pdf")
if err != nil {
    return err
}
defer r.Close()

for entry := range r.Objects() {
    s, ok := entry.Object.(*pdfdisassembler.Stream)
    if !ok {
        continue
    }
    ref := entry.Reference
    data, err := r.DecodeStream(ref)
    if err != nil {
        fmt.Printf("%d %d R: %v\n", ref.Number, ref.Generation, err)
        continue
    }
    fmt.Printf("%d %d R: %d bytes raw, %d bytes decoded\n",
        ref.Number, ref.Generation, s.RawLength(), len(data))
}
```

More complete examples live under [`examples/`](examples): `inspect`
prints a summary of a PDF, `structtree` walks the `/StructTreeRoot` as a
starting point for accessibility tooling, and `pageinfo` reports per-page
boxes, rotation, resources and content size via the page-tree API.

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
