// Command pdfdump emits a JSON snapshot of a PDF in the same format used
// by pdfdisassembler's snapshot-test harness.
//
// Usage:
//
//	pdfdump [-stream-content] [-no-preview] <file.pdf>
//
// Diff two PDFs structurally:
//
//	diff <(pdfdump a.pdf) <(pdfdump b.pdf)
//
// Reproduce a parser bug:
//
//	pdfdump broken.pdf > broken.json
//	# attach broken.pdf and broken.json to the issue
package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/speedata/pdfdisassembler"
)

func main() {
	inlineContent := flag.Bool("stream-content", false, "embed decoded stream bytes as hex under decoded.hex")
	noPreview := flag.Bool("no-preview", false, "omit the preview_utf8 field on streams")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: pdfdump [flags] <file.pdf>")
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}
	r, err := pdfdisassembler.OpenFile(flag.Arg(0))
	if err != nil {
		log.Fatal(err)
	}
	defer r.Close()

	opts := pdfdisassembler.DumpOptions{
		InlineStreamContent: *inlineContent,
	}
	if *noPreview {
		opts.PreviewMaxBytes = -1
	}
	data, err := pdfdisassembler.Dump(r, opts)
	if err != nil {
		log.Fatal(err)
	}
	if _, err := os.Stdout.Write(data); err != nil {
		log.Fatal(err)
	}
}
