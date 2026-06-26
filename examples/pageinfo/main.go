// Command pageinfo prints per-page geometry and content info for a PDF:
// page count and, for each page, its boxes, rotation, resource categories,
// and decoded content size. Intended as a starting point for page-importer
// and layout tooling.
package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"sort"

	"github.com/speedata/pdfdisassembler"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: pageinfo <file.pdf>")
		os.Exit(2)
	}
	r, err := pdfdisassembler.OpenFile(os.Args[1])
	if err != nil {
		log.Fatal(err)
	}
	defer r.Close()

	if err := report(os.Stdout, r); err != nil {
		log.Fatal(err)
	}
}

// report writes a human-readable page summary for r to w. The page tree is
// walked once via Reader.Pages; inherited attributes (boxes, rotation,
// resources) are resolved by the Page accessors.
func report(w io.Writer, r *pdfdisassembler.Reader) error {
	pages, err := r.Pages()
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "Pages: %d\n", len(pages))

	for _, p := range pages {
		// Display pages 1-based, matching how readers number them; the API
		// itself is 0-based (Page.Index).
		fmt.Fprintf(w, "Page %d:\n", p.Index()+1)

		if box, ok := p.Box(pdfdisassembler.MediaBox); ok {
			fmt.Fprintf(w, "  MediaBox: %g x %g pt\n", box.Width(), box.Height())
		}
		if box, ok := p.Box(pdfdisassembler.CropBox); ok {
			fmt.Fprintf(w, "  CropBox:  [%g %g %g %g]\n", box.LLX, box.LLY, box.URX, box.URY)
		}
		if rot := p.Rotation(); rot != 0 {
			fmt.Fprintf(w, "  Rotation: %d\n", rot)
		}
		if res, ok := p.Resources(); ok {
			keys := res.Keys()
			sort.Strings(keys)
			fmt.Fprintf(w, "  Resources: %v\n", keys)
		}

		content, err := p.Content()
		if err != nil {
			fmt.Fprintf(w, "  Content: (error: %v)\n", err)
			continue
		}
		fmt.Fprintf(w, "  Content: %d bytes decoded\n", len(content))
	}
	return nil
}
