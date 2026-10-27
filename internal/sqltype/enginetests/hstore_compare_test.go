package enginetests

import (
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/sqltype"
)

func hstoreComparisonValue(t *testing.T, d *sqltype.Descriptor, text string) sqltype.Value {
	t.Helper()
	v, err := d.Input(context.Background(), text, -1)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestHstoreComparison(t *testing.T) {
	ctx := context.Background()
	d := hstoreUnitType(t, 65000)
	p, err := sqltype.BindComparison(d, d, sqltype.CollationRef{}, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		left, right string
		want        int
	}{
		{"", "", 0}, {"", `""=>NULL`, -1},
		{"b=>0", "aa=>0", 1}, {"a=>2", "a=>10", 1},
		{"a=>b", "a=>ba", -1}, {"a=>bc", "ab=>c", -1},
		{"a=>bcd", "a=>b,c=>d", -1}, {"a=>NULL", `a=>""`, 1},
		{"a=>NULL", "a=>b", -1}, {"a=>NULL", `a=>"NULL"`, -1},
		{"a=>NULL,b=>c", `a=>"",b=>c`, 1},
		{"b=>2,a=>1", `"a"=>"1", "b"=>"2"`, 0},
		{"a=>1,a=>1", "a=>1", 0}, {"a=>é", "a=>z", 1},
		{"é=>0", "aa=>0", 1}, {`""=>"",a=>b`, `""=>"",a=>c`, -1},
		{`"a\"b"=>"c\\d"`, `"a\"b"=>"c\\e"`, -1},
		{"a=>" + strings.Repeat("x", 4096) + "y", "a=>" + strings.Repeat("x", 4096) + "z", -1},
	} {
		t.Run(tc.left+"/"+tc.right, func(t *testing.T) {
			a, b := hstoreComparisonValue(t, d, tc.left), hstoreComparisonValue(t, d, tc.right)
			for _, reverse := range []bool{false, true} {
				left, right, want := a, b, tc.want
				if reverse {
					left, right, want = b, a, -want
				}
				got, err := p.Compare(ctx, left, right, sqltype.InputEnvironment{})
				if err != nil || got != want {
					t.Fatalf("comparison %d/%v want %d", got, err, want)
				}
			}
			if got, err := p.Compare(ctx, a, a, sqltype.InputEnvironment{}); got != 0 || err != nil {
				t.Fatalf("reflexivity %d/%v", got, err)
			}
		})
	}
	a := hstoreComparisonValue(t, d, "a=>"+strings.Repeat("x", 8192))
	for _, v := range []sqltype.Value{sqltype.Null(d), {}, hstoreComparisonValue(t, hstoreUnitType(t, 65000), "a=>b")} {
		if _, err := p.Compare(ctx, a, v, sqltype.InputEnvironment{}); err == nil {
			t.Fatal("foreign or NULL comparison operand admitted")
		}
	}
	if _, err := sqltype.BindComparison(d, hstoreUnitType(t, 65000), sqltype.CollationRef{}, false); err == nil {
		t.Fatal("same-OID foreign descriptor admitted")
	}
	if equality, ordering := sqltype.ComparisonCapabilities(d); !equality || !ordering {
		t.Fatal("missing typed datum capability")
	}
	for op := sqltype.CompareEqual; op <= sqltype.CompareGreaterEqual; op++ {
		_, err := sqltype.ResolveComparison(op, d, d)
		if (op == sqltype.CompareEqual || op == sqltype.CompareNotEqual) != (err == nil) {
			t.Fatalf("ordinary operator admission %v: %v", op, err)
		}
		_, err = sqltype.ResolveComparison(op, nil, d)
		if (op == sqltype.CompareEqual || op == sqltype.CompareNotEqual) != (err == nil) {
			t.Fatalf("unknown operand operator admission %v: %v", op, err)
		}
	}
	if _, err := sqltype.BindOrdering(d.Usage()); err == nil {
		t.Fatal("builtin ordering bypassed installed class selection")
	}
	if allocs := testing.AllocsPerRun(100, func() {
		got, err := p.Compare(ctx, a, a, sqltype.InputEnvironment{})
		if got != 0 || err != nil {
			panic("comparison failed")
		}
	}); allocs != 0 {
		t.Fatalf("comparison allocated %g times", allocs)
	}
	base, cancel := context.WithCancel(ctx)
	defer cancel()
	reached := &hstoreCancelContext{Context: base, cancel: cancel, remaining: 8}
	if _, err := p.Compare(reached, a, a, sqltype.InputEnvironment{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("long-span reached cancellation: %v", err)
	}
	canceled, stop := context.WithCancel(ctx)
	stop()
	empty := hstoreComparisonValue(t, d, "")
	if _, err := p.Compare(canceled, empty, empty, sqltype.InputEnvironment{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("empty cancellation: %v", err)
	}
}

func TestHstoreComparisonOwnership(t *testing.T) {
	ctx := context.Background()
	source, target := hstoreUnitType(t, 65000), hstoreUnitType(t, 65001)
	a := hstoreComparisonValue(t, source, "b=>2,a=>1")
	mapped, err := a.RemapHstore(target)
	if err != nil {
		t.Fatal(err)
	}
	if same, err := a.SamePayload(ctx, mapped); !same || err != nil {
		t.Fatalf("remap lost immutable payload %v/%v", same, err)
	}
	if allocs := testing.AllocsPerRun(100, func() {
		var err error
		mapped, err = a.RemapHstore(target)
		if err != nil || mapped.Type() != target {
			panic("remap failed")
		}
	}); allocs != 0 {
		t.Fatalf("immutable payload remap allocated %g times", allocs)
	}
	sourceHash, err := sqltype.HstoreHash(ctx, a, 4294967296)
	if err != nil {
		t.Fatal(err)
	}
	targetHash, err := sqltype.HstoreHash(ctx, mapped, 4294967296)
	if err != nil || sourceHash != targetHash {
		t.Fatalf("remap changed canonical hash %d/%d: %v", sourceHash, targetHash, err)
	}
	if _, err := a.Retag(target); err == nil {
		t.Fatal("foreign generation retag admitted")
	}
	for _, v := range []sqltype.Value{a, mapped} {
		g, err := sqltype.BindGrouping(v.Type().Usage())
		if err != nil {
			t.Fatal(err)
		}
		b := hstoreComparisonValue(t, v.Type(), `"a"=>"1", "b"=>"2"`)
		if equal, err := g.Equal(ctx, v, b); !equal || err != nil {
			t.Fatalf("logical grouping equality %v/%v", equal, err)
		}
		h1, err := g.Hash(ctx, v)
		if err != nil {
			t.Fatal(err)
		}
		h2, err := g.Hash(ctx, b)
		if err != nil || h1 != h2 {
			t.Fatalf("equal grouping hashes %d/%d: %v", h1, h2, err)
		}
		if equal, err := g.Equal(ctx, sqltype.Null(v.Type()), sqltype.Null(v.Type())); !equal || err != nil {
			t.Fatalf("NULL group %v/%v", equal, err)
		}
		if equal, err := g.Equal(ctx, v, sqltype.Null(v.Type())); equal || err != nil {
			t.Fatalf("NULL versus present group %v/%v", equal, err)
		}
		for _, text := range []string{"a=>bc", "ab=>c", "a=>NULL", `a=>""`} {
			x := hstoreComparisonValue(t, v.Type(), text)
			if equal, err := g.Equal(ctx, v, x); equal || err != nil {
				t.Fatalf("unequal group %q %v/%v", text, equal, err)
			}
		}
	}
	g, err := sqltype.BindGrouping(source.Usage())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.Equal(ctx, a, mapped); err == nil {
		t.Fatal("source group admitted destination generation")
	}
	if _, err := g.Hash(ctx, mapped); err == nil {
		t.Fatal("source hash admitted destination generation")
	}
	before, err := source.Output(ctx, a)
	if err != nil || before != `"a"=>"1", "b"=>"2"` {
		t.Fatalf("source changed %q/%v", before, err)
	}
}
