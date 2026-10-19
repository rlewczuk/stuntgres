package sqltype

import (
	"context"
	"strings"
	"stuntgres/internal/engine/fts"
	"testing"
)

func ftsUnitType(t *testing.T, id ID) *Descriptor {
	t.Helper()
	for _, d := range Builtins() {
		if d.ID() == id {
			return d
		}
	}
	t.Fatalf("missing type %d", id)
	return nil
}
func TestFTSTypedValue(t *testing.T) {
	ctx := context.Background()
	for _, id := range []ID{TSVector, TSQuery} {
		d := ftsUnitType(t, id)
		v, err := d.Input(ctx, "a", -1)
		if err != nil || v.IsNull() || v.Type() != d || !v.validFTS() {
			t.Fatal(v, err)
		}
		fresh := ftsUnitType(t, id)
		copy, err := v.Retag(fresh)
		if err != nil || copy.Type() != fresh {
			t.Fatal(copy, err)
		}
		if v.ftsVector != copy.ftsVector || v.ftsQuery != copy.ftsQuery {
			t.Fatal("retag copied payload")
		}
		other, err := d.Input(ctx, "'a'", -1)
		if err != nil || !SameDatum(v, other) {
			t.Fatal("independent equal datum", err)
		}
		if same, err := v.SamePayload(ctx, copy); err != nil || !same {
			t.Fatal("retag payload", err)
		}
		if same, err := v.SamePayload(ctx, other); err != nil || !same {
			t.Fatal("independent payload", err)
		}
		if _, err := (Value{typ: d}).Retag(fresh); err == nil {
			t.Fatal("missing payload retagged")
		}
		if _, err := d.Output(ctx, Value{typ: d}); err == nil {
			t.Fatal("missing payload output")
		}
		zero := Value{typ: d}
		if id == TSVector {
			zero.ftsVector = new(fts.Vector)
		} else {
			zero.ftsQuery = new(fts.Query)
		}
		if _, err := d.Output(ctx, zero); err == nil {
			t.Fatal("zero payload output")
		}
		if _, err := zero.Retag(fresh); err == nil {
			t.Fatal("zero payload retagged")
		}
		empty, err := d.Input(ctx, "", -1)
		if err != nil || empty.IsNull() {
			t.Fatal(empty, err)
		}
		out, err := d.Output(ctx, empty)
		if err != nil || out != "" {
			t.Fatal(out, err)
		}
		null := Null(d)
		if !null.IsNull() || !SameDatum(null, Null(d)) || SameDatum(null, empty) {
			t.Fatal("typed NULL identity")
		}
		if _, err := d.Input(ctx, "a", 2); err == nil {
			t.Fatal("modifier admitted")
		}
		text := ftsUnitType(t, Text)
		plan, err := PlanCoercion(text.Usage(), d.Usage(), Explicit)
		if err != nil {
			t.Fatal(err)
		}
		value, err := plan.Apply(ctx, String(text, "a"), InputEnvironment{}, OutputEnvironment{})
		if err != nil || !SameDatum(value, v) {
			t.Fatal(value, err)
		}
		quoted, err := d.Input(ctx, "'a''b'", -1)
		if err != nil {
			t.Fatal(err)
		}
		literal, err := d.SQLLiteral(ctx, quoted)
		if err != nil || literal != "'''a''''b'''" {
			t.Fatal(literal, err)
		}
		if _, err := d.Input(ctx, "a\x00", -1); err == nil {
			t.Fatal("NUL accepted")
		}
		if _, err := d.Input(ctx, string([]byte{0xff}), -1); err == nil {
			t.Fatal("invalid UTF8 accepted")
		}
	}
	d := ftsUnitType(t, TSQuery)
	left, _ := d.Input(ctx, "(a & b) & c", -1)
	right, _ := d.Input(ctx, "a & (b & c)", -1)
	lo, _ := d.Output(ctx, left)
	ro, _ := d.Output(ctx, right)
	if lo != ro {
		t.Fatal("same-text structural fixture")
	}
	if SameDatum(left, right) {
		t.Fatal("association lost")
	}
	if same, _ := left.SamePayload(ctx, right); same {
		t.Fatal("payload association lost")
	}
	if _, err := left.Retag(ftsUnitType(t, TSVector)); err == nil {
		t.Fatal("foreign family retagged")
	}
	for _, input := range []string{"a & (b & c)", "a | (b | c)"} {
		before, err := d.Input(ctx, input, -1)
		if err != nil {
			t.Fatal(err)
		}
		literal, err := d.SQLLiteral(ctx, before)
		if err != nil {
			t.Fatal(err)
		}
		decoded := strings.ReplaceAll(literal[1:len(literal)-1], "''", "'")
		after, err := d.Input(ctx, decoded, -1)
		if err != nil || !SameDatum(before, after) {
			t.Fatal("SQLLiteral structure", literal, err)
		}
		if same, err := before.SamePayload(ctx, after); err != nil || !same {
			t.Fatal("SQLLiteral payload", err)
		}
	}
}
func TestFTSTypedMetadata(t *testing.T) {
	for _, tc := range []struct {
		id            ID
		name, storage string
		analyze       uint32
	}{{TSVector, "tsvector", "x", 3688}, {TSQuery, "tsquery", "p", 0}} {
		d := ftsUnitType(t, tc.id)
		m, ok := d.PGTypeMetadata()
		if !ok || d.Name() != tc.name || d.DatabaseName() != strings.ToUpper(tc.name) || m.Kind != "b" || m.Category != "U" || m.Length != -1 || m.ByValue || m.Alignment != "i" || m.Storage != tc.storage || m.Analyze != tc.analyze || m.Collation != 0 || m.Modifier != -1 {
			t.Fatal(d, m, ok)
		}
	}
	for _, tc := range []struct{ id, element ID }{{3643, TSVector}, {3645, TSQuery}} {
		d := ftsUnitType(t, tc.id)
		m, ok := d.PGTypeMetadata()
		if !ok || d.Concrete() || m.Element != tc.element || m.Category != "A" || m.Analyze != 3816 || m.Input != 750 || m.Output != 751 {
			t.Fatal("companion metadata/execution boundary", m, ok)
		}
	}
}
func TestFTSTypedScope(t *testing.T) {
	for _, id := range []ID{TSVector, TSQuery} {
		d := ftsUnitType(t, id)
		if !d.Concrete() || !StoredScalarCastType(id) {
			t.Fatal("scalar not executable")
		}
		for _, other := range []ID{TSVector, TSQuery, Int4} {
			if id == other {
				continue
			}
			if _, err := PlanCoercion(d.Usage(), ftsUnitType(t, other).Usage(), Explicit); err == nil {
				t.Fatal("unsupported cast admitted", id, other)
			}
		}
	}
	for _, id := range []ID{TSVector, TSQuery} {
		d := ftsUnitType(t, id)
		if _, err := BindComparison(d, d, CollationRef{}, false); err == nil {
			t.Fatal("FTS comparison admitted")
		}
		if _, ok := DefaultIndexClass(d); ok {
			t.Fatal("FTS default index class admitted")
		}
	}
	for _, id := range []ID{3643, 3645} {
		d := ftsUnitType(t, id)
		if d.Concrete() {
			t.Fatal("FTS array executable")
		}
		if _, err := d.Input(context.Background(), "{}", -1); err == nil {
			t.Fatal("FTS array input admitted")
		}
	}
}
