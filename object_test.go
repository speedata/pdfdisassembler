package pdfdisassembler

import (
	"bytes"
	"testing"
)

func TestDictAccessors(t *testing.T) {
	data := buildDictPDF(t, []string{
		"<< /Type /Catalog /Pages 2 0 R /IntVal 42 /NegInt -7 /BoolVal true " +
			"/StrVal (hi) /NameVal /Foo /ArrVal [ 1 2 3 ] /DictRef 3 0 R >>",
		"<< /Type /Pages /Kids [] /Count 0 >>",
		"<< /Inner (deep) >>",
	})
	r, err := Open(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer r.Close()
	cat, err := r.Catalog()
	if err != nil {
		t.Fatalf("Catalog: %v", err)
	}

	if v, ok := cat.Int("IntVal"); !ok || v != 42 {
		t.Errorf("Int(IntVal) = %d, %v", v, ok)
	}
	if v, ok := cat.Int("NegInt"); !ok || v != -7 {
		t.Errorf("Int(NegInt) = %d, %v", v, ok)
	}
	if v, ok := cat.Bool("BoolVal"); !ok || !v {
		t.Errorf("Bool(BoolVal) = %v, %v", v, ok)
	}
	if v, ok := cat.String("StrVal"); !ok || v != "hi" {
		t.Errorf("String(StrVal) = %q, %v", v, ok)
	}
	if v, ok := cat.Bytes("StrVal"); !ok || string(v) != "hi" {
		t.Errorf("Bytes(StrVal) = %q, %v", v, ok)
	}
	if v, ok := cat.Name("NameVal"); !ok || v != "Foo" {
		t.Errorf("Name(NameVal) = %q, %v", v, ok)
	}
	if v, ok := cat.Array("ArrVal"); !ok || len(v) != 3 {
		t.Errorf("Array(ArrVal) len = %d, %v", len(v), ok)
	}

	// Dict() follows the indirect reference to object 3.
	inner, ok := cat.Dict("DictRef")
	if !ok {
		t.Fatal("Dict(DictRef) not resolved")
	}
	if s, ok := inner.String("Inner"); !ok || s != "deep" {
		t.Errorf("resolved inner String(Inner) = %q, %v", s, ok)
	}

	if !cat.Has("IntVal") || cat.Has("Missing") {
		t.Error("Has wrong")
	}
	if cat.Len() < 8 {
		t.Errorf("Len = %d, want >= 8", cat.Len())
	}
	seen := map[string]bool{}
	for _, k := range cat.Keys() {
		seen[k] = true
	}
	for k := range cat.Iter() {
		if !seen[k] {
			t.Errorf("Iter yielded %q absent from Keys", k)
		}
	}
	if !seen["IntVal"] || !seen["ArrVal"] {
		t.Errorf("Keys missing entries: %v", cat.Keys())
	}

	// Type mismatches must report ok=false, not panic or coerce.
	if _, ok := cat.Int("StrVal"); ok {
		t.Error("Int on a string")
	}
	if _, ok := cat.Bool("IntVal"); ok {
		t.Error("Bool on an int")
	}
	if _, ok := cat.Name("IntVal"); ok {
		t.Error("Name on an int")
	}
	if _, ok := cat.Array("IntVal"); ok {
		t.Error("Array on an int")
	}
	if _, ok := cat.Dict("IntVal"); ok {
		t.Error("Dict on an int")
	}
	if _, ok := cat.Stream("IntVal"); ok {
		t.Error("Stream on an int")
	}
	if _, ok := cat.String("IntVal"); ok {
		t.Error("String on an int")
	}
	if _, ok := cat.Bytes("IntVal"); ok {
		t.Error("Bytes on an int")
	}
	if _, ok := cat.Int("Missing"); ok {
		t.Error("Int on a missing key")
	}
}

// Every accessor must be safe on a nil *Dict (the common "key absent" result).
func TestDictNilReceiver(t *testing.T) {
	var d *Dict
	if d.Len() != 0 {
		t.Error("Len")
	}
	if d.Has("x") {
		t.Error("Has")
	}
	if d.Keys() != nil {
		t.Error("Keys")
	}
	if _, ok := d.Get("x"); ok {
		t.Error("Get")
	}
	for range d.Iter() {
		t.Error("Iter on nil yielded an entry")
	}
}

// The typed getters must miss (ok=false) — not coerce or fabricate a zero — on
// a missing key, a wrong type, or an unresolvable Reference (nil reader).
func TestDictTypedGetterMisses(t *testing.T) {
	d := newDict(nil)
	d.set("i", Integer(5))
	d.set("b", Bool(true))
	d.set("s", String("hi"))

	wrongType := []struct {
		name string
		ok   bool
	}{
		{"Bool", func() bool { _, ok := d.Bool("i"); return ok }()},
		{"Int", func() bool { _, ok := d.Int("b"); return ok }()},
		{"Array", func() bool { _, ok := d.Array("i"); return ok }()},
		{"Dict", func() bool { _, ok := d.Dict("i"); return ok }()},
		{"Stream", func() bool { _, ok := d.Stream("i"); return ok }()},
		{"String", func() bool { _, ok := d.String("i"); return ok }()},
		{"Bytes", func() bool { _, ok := d.Bytes("i"); return ok }()},
	}
	for _, tc := range wrongType {
		if tc.ok {
			t.Errorf("%s on a wrong-typed value should miss", tc.name)
		}
	}

	missing := []bool{
		func() bool { _, ok := d.Int("x"); return ok }(),
		func() bool { _, ok := d.Bool("x"); return ok }(),
		func() bool { _, ok := d.Array("x"); return ok }(),
		func() bool { _, ok := d.Dict("x"); return ok }(),
		func() bool { _, ok := d.Stream("x"); return ok }(),
		func() bool { _, ok := d.String("x"); return ok }(),
		func() bool { _, ok := d.Bytes("x"); return ok }(),
	}
	for i, ok := range missing {
		if ok {
			t.Errorf("getter %d on a missing key should miss", i)
		}
	}

	// A Reference with no backing reader can't be dereferenced.
	d.set("ref", Reference{Number: 9})
	if _, ok := d.Int("ref"); ok {
		t.Error("Reference with nil reader should miss")
	}

	// Controls: the right type resolves.
	if v, ok := d.Int("i"); !ok || v != 5 {
		t.Errorf("Int(i) = %d, %v; want 5, true", v, ok)
	}
	if s, ok := d.Bytes("s"); !ok || string(s) != "hi" {
		t.Errorf("Bytes(s) = %q, %v; want hi, true", s, ok)
	}
}
