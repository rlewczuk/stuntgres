package sqltype

import (
	"context"
	"errors"
	"testing"
)

func TestGroupingEnumOrdinalDomainsAndRecursiveArrays(t *testing.T) {
	ctx := context.Background()
	enum, err := Enum(62000, "group_enum", "public.group_enum", []string{"z", "a", "q"})
	if err != nil {
		t.Fatal(err)
	}
	domain, err := Domain(62001, "group_enum_domain", "public.group_enum_domain", enum.Usage())
	if err != nil {
		t.Fatal(err)
	}
	array, err := Array(62002, "_group_enum_domain", domain)
	if err != nil {
		t.Fatal(err)
	}
	whole, err := Domain(62003, "group_enum_array", "public.group_enum_array", array.Usage())
	if err != nil {
		t.Fatal(err)
	}
	nested, err := Array(62004, "_group_enum_array", whole)
	if err != nil {
		t.Fatal(err)
	}
	outer, err := Domain(62005, "group_enum_nested", "public.group_enum_nested", nested.Usage())
	if err != nil {
		t.Fatal(err)
	}
	for _, sample := range []struct {
		typ   *Descriptor
		a, b  string
		equal bool
	}{
		{enum, "z", "z", true}, {enum, "z", "a", false}, {domain, "a", "a", true}, {domain, "a", "q", false},
		{array, "{z,NULL}", "{z,NULL}", true}, {array, "{z,NULL}", "[0:1]={z,NULL}", false}, {whole, "{z,NULL}", "{a,NULL}", false},
		{outer, `{"{z,NULL}",NULL}`, `{"{z,NULL}",NULL}`, true}, {outer, `{"{z,NULL}",NULL}`, `{"{a,NULL}",NULL}`, false},
	} {
		g, err := BindGrouping(sample.typ.Usage())
		if err != nil {
			t.Fatal(sample.typ, err)
		}
		a, err := sample.typ.Input(ctx, sample.a, -1)
		if err != nil {
			t.Fatal(err)
		}
		b, err := sample.typ.Input(ctx, sample.b, -1)
		if err != nil {
			t.Fatal(err)
		}
		equal, err := g.Equal(ctx, a, b)
		if err != nil || equal != sample.equal {
			t.Fatal(sample, equal, err)
		}
		ah, err := g.Hash(ctx, a)
		if err != nil {
			t.Fatal(err)
		}
		bh, err := g.Hash(ctx, b)
		if err != nil {
			t.Fatal(err)
		}
		if equal && ah != bh {
			t.Fatal("equal enum key hashes differ", sample, ah, bh)
		}
		if equal, err := g.Equal(ctx, Null(sample.typ), Null(sample.typ)); err != nil || !equal {
			t.Fatal(equal, err)
		}
		if equal, err := g.Equal(ctx, Null(sample.typ), a); err != nil || equal {
			t.Fatal(equal, err)
		}
	}
	for _, typ := range []*Descriptor{enum, domain} {
		g, err := BindGrouping(typ.Usage())
		if err != nil {
			t.Fatal(err)
		}
		for _, ordinal := range []int64{-1, 3} {
			value := Value{typ: typ, integer: ordinal}
			if _, err := g.Hash(ctx, value); err == nil {
				t.Fatal("invalid enum ordinal hashed", typ, ordinal)
			}
			if _, err := g.Equal(ctx, value, value); err == nil {
				t.Fatal("invalid enum ordinal compared", typ, ordinal)
			}
		}
	}
	foreign, err := Enum(enum.ID(), enum.Name(), enum.SQLName(), []string{"z", "a", "q"})
	if err != nil {
		t.Fatal(err)
	}
	value, err := foreign.Input(ctx, "z", -1)
	if err != nil {
		t.Fatal(err)
	}
	g, err := BindGrouping(enum.Usage())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.Hash(ctx, value); err == nil {
		t.Fatal("foreign enum generation hashed")
	}
	if _, err := g.Equal(ctx, value, value); err == nil {
		t.Fatal("foreign enum generation compared")
	}
	if _, err := BindComparison(enum, foreign); err == nil {
		t.Fatal("ordinary enum identity widened")
	}
	if _, err := BindComparison(domain, domain); err == nil {
		t.Fatal("ordinary polymorphic enum domain operands widened")
	}
	if _, err := BindComparison(array, array); err == nil {
		t.Fatal("ordinary enum-array comparison widened")
	}
	if _, err := BindOrdering(array.Usage()); err == nil {
		t.Fatal("ordinary enum-array ordering widened")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	key, err := outer.Input(ctx, `{"{z,NULL}",NULL}`, -1)
	if err != nil {
		t.Fatal(err)
	}
	recursive, err := BindGrouping(outer.Usage())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := recursive.Hash(cancelled, key); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := recursive.Equal(cancelled, key, key); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	empty, err := Enum(62007, "group_empty_enum", "public.group_empty_enum", nil)
	if err != nil {
		t.Fatal(err)
	}
	emptyKey, err := BindGrouping(empty.Usage())
	if err != nil {
		t.Fatal(err)
	}
	if equal, err := emptyKey.Equal(ctx, Null(empty), Null(empty)); err != nil || !equal {
		t.Fatal(equal, err)
	}
	if _, err := emptyKey.Hash(ctx, Null(empty)); err != nil {
		t.Fatal(err)
	}
}
