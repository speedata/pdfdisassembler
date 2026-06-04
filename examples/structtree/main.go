// Command structtree dumps the /StructTreeRoot of a PDF in indented form.
// Intended as a starting point for accessibility-checker tooling.
package main

import (
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/speedata/pdfdisassembler"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: structtree <file.pdf>")
		os.Exit(2)
	}
	r, err := pdfdisassembler.OpenFile(os.Args[1])
	if err != nil {
		log.Fatal(err)
	}
	defer r.Close()

	cat, err := r.Catalog()
	if err != nil {
		log.Fatal(err)
	}
	root, ok := cat.Dict("StructTreeRoot")
	if !ok {
		fmt.Println("(no StructTreeRoot)")
		return
	}

	roleMap := map[string]string{}
	if rm, ok := root.Dict("RoleMap"); ok {
		for k, v := range rm.Iter() {
			if n, ok := v.(pdfdisassembler.Name); ok {
				roleMap[k] = string(n)
			}
		}
	}

	walk(r, root, roleMap, 0)
}

func walk(r *pdfdisassembler.Reader, node *pdfdisassembler.Dict, roleMap map[string]string, depth int) {
	indent := strings.Repeat("  ", depth)
	typeName, _ := node.Name("S")
	if typeName == "" {
		typeName, _ = node.Name("Type")
	}
	role := string(typeName)
	if mapped, ok := roleMap[role]; ok {
		role = role + " -> " + mapped
	}
	fmt.Printf("%s%s\n", indent, role)

	k, ok := node.Get("K")
	if !ok {
		return
	}
	if ref, ok := k.(pdfdisassembler.Reference); ok {
		if v, err := r.Resolve(ref); err == nil {
			k = v
		}
	}
	switch t := k.(type) {
	case pdfdisassembler.Array:
		for _, child := range t {
			if d, err := r.ResolveDict(child); err == nil {
				walk(r, d, roleMap, depth+1)
			}
		}
	case *pdfdisassembler.Dict:
		walk(r, t, roleMap, depth+1)
	}
}
