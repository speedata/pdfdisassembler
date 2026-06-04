package pdfdisassembler

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// updateGoldens regenerates fixture golden.json files instead of comparing.
// Run as: go test -update -run TestFixtures.
var updateGoldens = flag.Bool("update", false, "regenerate fixture golden.json files")

// TestFixtures iterates every directory under testdata/fixtures, opens
// its input.pdf, and compares Dump output against the committed
// golden.json. A test failure prints the first few differing lines and
// the command to regenerate the golden.
//
// Adding a fixture:
//
//  1. Create testdata/fixtures/<name>/input.pdf (real-world or generated)
//  2. go test -update -run TestFixtures/<name>
//  3. Inspect golden.json — does it match what the spec says should happen?
//  4. Commit input.pdf, golden.json, and (optionally) README.md describing
//     what the fixture proves.
func TestFixtures(t *testing.T) {
	root := "testdata/fixtures"
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Skipf("no testdata/fixtures: %v", err)
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		t.Run(name, func(t *testing.T) {
			runFixture(t, filepath.Join(root, name))
		})
	}
}

func runFixture(t *testing.T, dir string) {
	t.Helper()
	inputPath := filepath.Join(dir, "input.pdf")
	goldenPath := filepath.Join(dir, "golden.json")

	r, err := OpenFile(inputPath)
	if err != nil {
		t.Fatalf("Open %s: %v", inputPath, err)
	}
	defer r.Close()

	got, err := Dump(r, DumpOptions{})
	if err != nil {
		t.Fatalf("Dump: %v", err)
	}

	if *updateGoldens {
		if err := os.WriteFile(goldenPath, got, 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		t.Logf("updated %s (%d bytes)", goldenPath, len(got))
		return
	}

	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden %s: %v\n\tregenerate with: go test -update -run %s",
			goldenPath, err, t.Name())
	}
	if bytes.Equal(got, want) {
		return
	}
	t.Errorf("dump mismatch\n  regenerate: go test -update -run %s\n  first diffs:\n%s",
		t.Name(), firstDiffLines(want, got, 10))
}

// firstDiffLines returns at most maxLines of unified-style "-want / +got"
// hints around line-level differences.
func firstDiffLines(want, got []byte, maxLines int) string {
	wl := strings.Split(string(want), "\n")
	gl := strings.Split(string(got), "\n")
	max := len(wl)
	if len(gl) > max {
		max = len(gl)
	}
	var out strings.Builder
	shown := 0
	for i := 0; i < max && shown < maxLines; i++ {
		var w, g string
		if i < len(wl) {
			w = wl[i]
		}
		if i < len(gl) {
			g = gl[i]
		}
		if w == g {
			continue
		}
		fmt.Fprintf(&out, "    line %d:\n      - %s\n      + %s\n", i+1, w, g)
		shown++
	}
	return out.String()
}
