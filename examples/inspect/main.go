// Command inspect prints a summary of a PDF: version, document info,
// catalog top-level keys, page count.
package main

import (
	"fmt"
	"log"
	"os"

	"github.com/speedata/pdfdisassembler"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: inspect <file.pdf>")
		os.Exit(2)
	}
	r, err := pdfdisassembler.OpenFile(os.Args[1])
	if err != nil {
		log.Fatal(err)
	}
	defer r.Close()

	fmt.Printf("PDF version: %s\n", r.Version())

	info := r.DocumentInfo()
	if info.Title != "" {
		fmt.Printf("Title:    %s\n", info.Title)
	}
	if info.Author != "" {
		fmt.Printf("Author:   %s\n", info.Author)
	}
	if info.Producer != "" {
		fmt.Printf("Producer: %s\n", info.Producer)
	}
	if !info.CreationDate.IsZero() {
		fmt.Printf("Created:  %s\n", info.CreationDate.Format("2006-01-02 15:04:05"))
	}

	cat, err := r.Catalog()
	if err != nil {
		log.Fatalf("catalog: %v", err)
	}
	fmt.Println("Catalog keys:")
	for k := range cat.Iter() {
		fmt.Printf("  /%s\n", k)
	}

	if pages, ok := cat.Dict("Pages"); ok {
		if n, ok := pages.Int("Count"); ok {
			fmt.Printf("Pages: %d\n", n)
		}
	}

	count := 0
	for range r.Objects() {
		count++
	}
	fmt.Printf("Live indirect objects: %d\n", count)
}
