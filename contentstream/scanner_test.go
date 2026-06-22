package contentstream_test

import (
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/speedata/pdfdisassembler/contentstream"
)

func collect(t *testing.T, src string) []contentstream.Op {
	t.Helper()
	var out []contentstream.Op
	sc := contentstream.New([]byte(src))
	for {
		op, err := sc.Next()
		if errors.Is(err, io.EOF) {
			return out
		}
		if err != nil {
			t.Fatalf("scan error: %v", err)
		}
		out = append(out, op)
	}
}

func TestEmpty(t *testing.T) {
	if got := collect(t, ""); len(got) != 0 {
		t.Fatalf("want 0 ops, got %d", len(got))
	}
	if got := collect(t, "   \n\t  "); len(got) != 0 {
		t.Fatalf("want 0 ops on whitespace-only, got %d", len(got))
	}
}

func TestSimpleOperators(t *testing.T) {
	src := "q 1 0 0 1 100 200 cm Q"
	ops := collect(t, src)
	if len(ops) != 3 {
		t.Fatalf("want 3 ops, got %d (%+v)", len(ops), ops)
	}
	if ops[0].Operator != "q" || len(ops[0].Operands) != 0 {
		t.Errorf("op[0] = %+v, want q with no operands", ops[0])
	}
	if ops[1].Operator != "cm" || len(ops[1].Operands) != 6 {
		t.Errorf("op[1] = %+v, want cm with 6 operands", ops[1])
	}
	if ops[2].Operator != "Q" {
		t.Errorf("op[2].Operator = %q, want Q", ops[2].Operator)
	}
}

func TestTfOperator(t *testing.T) {
	src := "BT /F1 12 Tf (Hello) Tj ET"
	ops := collect(t, src)
	if len(ops) != 4 {
		t.Fatalf("want 4 ops, got %d", len(ops))
	}
	if ops[0].Operator != "BT" || ops[3].Operator != "ET" {
		t.Errorf("BT/ET framing missing: %+v", ops)
	}
	if ops[1].Operator != "Tf" {
		t.Fatalf("op[1].Operator = %q, want Tf", ops[1].Operator)
	}
	if ops[1].Operands[0].Kind != contentstream.KindName || ops[1].Operands[0].Name != "F1" {
		t.Errorf("Tf font operand = %+v, want name F1", ops[1].Operands[0])
	}
	if ops[1].Operands[1].Kind != contentstream.KindNumber || ops[1].Operands[1].Number != 12 {
		t.Errorf("Tf size operand = %+v, want number 12", ops[1].Operands[1])
	}
	if ops[2].Operator != "Tj" {
		t.Fatalf("op[2].Operator = %q, want Tj", ops[2].Operator)
	}
	if string(ops[2].Operands[0].Bytes) != "Hello" {
		t.Errorf("Tj string = %q, want Hello", ops[2].Operands[0].Bytes)
	}
}

func TestTJArray(t *testing.T) {
	src := "[(He) -10 (l) -5 (lo)] TJ"
	ops := collect(t, src)
	if len(ops) != 1 {
		t.Fatalf("want 1 op, got %d", len(ops))
	}
	if ops[0].Operator != "TJ" {
		t.Fatalf("op.Operator = %q, want TJ", ops[0].Operator)
	}
	arr := ops[0].Operands[0]
	if arr.Kind != contentstream.KindArray {
		t.Fatalf("TJ operand kind = %v, want Array", arr.Kind)
	}
	if len(arr.Array) != 5 {
		t.Fatalf("TJ array len = %d, want 5", len(arr.Array))
	}
	if string(arr.Array[0].Bytes) != "He" || arr.Array[1].Number != -10 {
		t.Errorf("TJ contents off: %+v", arr.Array)
	}
}

func TestBDCInlineDict(t *testing.T) {
	src := "/Span << /MCID 7 /Lang (en-US) >> BDC (text) Tj EMC"
	ops := collect(t, src)
	if len(ops) != 3 {
		t.Fatalf("want 3 ops, got %d", len(ops))
	}
	if ops[0].Operator != "BDC" {
		t.Fatalf("op[0].Operator = %q, want BDC", ops[0].Operator)
	}
	if ops[0].Operands[0].Kind != contentstream.KindName || ops[0].Operands[0].Name != "Span" {
		t.Errorf("BDC tag = %+v, want name Span", ops[0].Operands[0])
	}
	props := ops[0].Operands[1]
	if props.Kind != contentstream.KindDict {
		t.Fatalf("BDC props kind = %v, want Dict", props.Kind)
	}
	mcid, ok := props.Dict["MCID"]
	if !ok {
		t.Fatalf("MCID missing from %+v", props.Dict)
	}
	if n, ok := mcid.Int(); !ok || n != 7 {
		t.Errorf("MCID = %v (intOk=%v), want 7", n, ok)
	}
	if ops[2].Operator != "EMC" {
		t.Errorf("op[2].Operator = %q, want EMC", ops[2].Operator)
	}
}

func TestBDCPropertyNameRef(t *testing.T) {
	src := "/Artifact /P1 BDC (x) Tj EMC"
	ops := collect(t, src)
	if len(ops) != 3 {
		t.Fatalf("want 3 ops, got %d", len(ops))
	}
	if ops[0].Operator != "BDC" {
		t.Fatalf("op[0].Operator = %q, want BDC", ops[0].Operator)
	}
	if ops[0].Operands[0].Name != "Artifact" {
		t.Errorf("tag = %q, want Artifact", ops[0].Operands[0].Name)
	}
	if ops[0].Operands[1].Kind != contentstream.KindName || ops[0].Operands[1].Name != "P1" {
		t.Errorf("properties ref = %+v, want name P1", ops[0].Operands[1])
	}
}

func TestHexString(t *testing.T) {
	src := "<48656C6C6F> Tj"
	ops := collect(t, src)
	if len(ops) != 1 {
		t.Fatalf("want 1 op, got %d", len(ops))
	}
	if string(ops[0].Operands[0].Bytes) != "Hello" {
		t.Errorf("hex Tj = %q, want Hello", ops[0].Operands[0].Bytes)
	}
}

func TestInlineImage(t *testing.T) {
	// BI /W 2 /H 2 /CS /G /BPC 8 ID
	// 4 raw bytes (\x00\x01\x02\x03) then EI
	src := "BI /W 2 /H 2 /CS /G /BPC 8 ID \x00\x01\x02\x03\nEI Q"
	ops := collect(t, src)
	if len(ops) != 2 {
		t.Fatalf("want 2 ops, got %d (%+v)", len(ops), ops)
	}
	if ops[0].Operator != "EI" {
		t.Fatalf("op[0].Operator = %q, want EI", ops[0].Operator)
	}
	if !reflect.DeepEqual(ops[0].Image, []byte{0, 1, 2, 3}) {
		t.Errorf("inline image bytes = % x, want 00 01 02 03", ops[0].Image)
	}
	if ops[1].Operator != "Q" {
		t.Errorf("op[1].Operator = %q, want Q", ops[1].Operator)
	}
}

func TestNumberInt(t *testing.T) {
	src := "42 3.14 0 Tr"
	ops := collect(t, src)
	if len(ops) != 1 {
		t.Fatalf("want 1 op, got %d", len(ops))
	}
	if n, ok := ops[0].Operands[0].Int(); !ok || n != 42 {
		t.Errorf("int %v ok=%v, want 42", n, ok)
	}
	if _, ok := ops[0].Operands[1].Int(); ok {
		t.Errorf("real should not yield Int()")
	}
}

func TestAllIteratorStopsOnError(t *testing.T) {
	src := "<<unbalanced"
	sc := contentstream.New([]byte(src))
	gotErr := false
	for _, err := range sc.All() {
		if err != nil {
			gotErr = true
		}
	}
	if !gotErr {
		t.Fatalf("expected an error from malformed input")
	}
}

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

// A flood of operands before an operator — directly or inside one array —
// must be rejected rather than accumulated unboundedly.
func TestScannerOperandFloodRejected(t *testing.T) {
	for name, src := range map[string]string{
		"bare":  strings.Repeat("1 ", 200000) + "n",
		"array": "[" + strings.Repeat("1 ", 200000) + "] n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := contentstream.New([]byte(src)).Next(); err == nil {
				t.Fatal("expected an operand-flood error, got nil")
			}
		})
	}
}

// FuzzScanner asserts the content-stream scanner never panics: Next may error,
// but must not crash on arbitrary input.
func FuzzScanner(f *testing.F) {
	f.Add([]byte("q 1 0 0 1 0 0 cm /F1 12 Tf (hi) Tj [(a) -5 (b)] TJ BI /W 1 ID xx EI Q"))
	f.Add([]byte("/Span << /MCID 7 >> BDC EMC"))
	f.Fuzz(func(t *testing.T, data []byte) {
		sc := contentstream.New(data)
		for i := 0; i <= len(data); i++ {
			if _, err := sc.Next(); err != nil {
				break
			}
		}
	})
}
