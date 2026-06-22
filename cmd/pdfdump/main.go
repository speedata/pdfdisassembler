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
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"

	"github.com/speedata/pdfdisassembler"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		log.Fatal(err)
	}
}

func run(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("pdfdump", flag.ContinueOnError)
	inlineContent := fs.Bool("stream-content", false, "embed decoded stream bytes as hex under decoded.hex")
	noPreview := fs.Bool("no-preview", false, "omit the preview_utf8 field on streams")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: pdfdump [flags] <file.pdf>")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return errors.New("pdfdump: exactly one input file is required")
	}
	r, err := pdfdisassembler.OpenFile(fs.Arg(0))
	if err != nil {
		return err
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
		return err
	}
	_, err = stdout.Write(data)
	return err
}
