package contentstream_test

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/speedata/pdfdisassembler/contentstream"
)

// An inline image with no data between ID and EI must yield an empty-image EI
// op, not panic on a reversed slice bound.
func TestInlineImageEmptyNoPanic(t *testing.T) {
	for _, src := range []string{"BI ID EI", "BI /W 1 /H 1 ID EI", "q BI ID\nEI Q"} {
		t.Run(src, func(t *testing.T) {
			sc := contentstream.New([]byte(src))
			var sawEI bool
			for {
				op, err := sc.Next()
				if errors.Is(err, io.EOF) {
					break
				}
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if op.Operator == "EI" {
					sawEI = true
					if len(op.Image) != 0 {
						t.Errorf("empty inline image: got %d image bytes", len(op.Image))
					}
				}
			}
			if !sawEI {
				t.Error("no EI op produced")
			}
		})
	}
}

// Deeply nested arrays must be rejected with an error rather than recursing
// until the goroutine stack overflows.
func TestDeeplyNestedArrayRejected(t *testing.T) {
	src := strings.Repeat("[", 5000) + strings.Repeat("]", 5000) + " n"
	sc := contentstream.New([]byte(src))
	if _, err := sc.Next(); err == nil {
		t.Fatal("expected a nesting-depth error, got nil")
	}
}

// Deeply nested dicts (via inline-image / BDC bodies) must likewise be bounded.
func TestDeeplyNestedDictRejected(t *testing.T) {
	src := "/P " + strings.Repeat("<< /K ", 5000) + "0" + strings.Repeat(" >>", 5000) + " BDC"
	sc := contentstream.New([]byte(src))
	if _, err := sc.Next(); err == nil {
		t.Fatal("expected a nesting-depth error, got nil")
	}
}

// Control: moderate nesting must still resolve, proving the limit doesn't
// reject legitimate content.
func TestModeratelyNestedArrayResolves(t *testing.T) {
	const depth = 100
	src := strings.Repeat("[", depth) + strings.Repeat("]", depth) + " n"
	sc := contentstream.New([]byte(src))
	op, err := sc.Next()
	if err != nil {
		t.Fatalf("unexpected error at depth %d: %v", depth, err)
	}
	if op.Operator != "n" || len(op.Operands) != 1 || op.Operands[0].Kind != contentstream.KindArray {
		t.Fatalf("want n op with one array operand, got %+v", op)
	}
}
