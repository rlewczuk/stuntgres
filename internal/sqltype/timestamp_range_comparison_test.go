package sqltype

import (
	"context"
	"testing"
)

func TestRangeOperatorComparisonAdmission(t *testing.T) {
	types := testTypes()
	ctx := context.Background()
	for _, id := range []ID{Int4Range, Int8Range, NumRange, DateRange, TimestampRange, TimestamptzRange, Int4Multirange, Int8Multirange, NumMultirange} {
		d := types[id]
		domain, err := Domain(20000, "span", "span", d.Usage())
		if err != nil {
			t.Fatal(err)
		}
		nested, err := Domain(20001, "chain", "chain", domain.Usage())
		if err != nil {
			t.Fatal(err)
		}
		for _, pair := range [][2]*Descriptor{{d, d}, {domain, domain}, {nested, nested}, {domain, d}, {d, nested}, {domain, nested}, {nested, domain}} {
			if err := CheckComparisonTypes(pair[0], pair[1]); err != nil {
				t.Fatal(id, err)
			}
			p, err := BindComparison(pair[0], pair[1], CollationRef{}, false)
			if err != nil {
				t.Fatal(id, err)
			}
			input := "empty"
			if isMultirange(id) {
				input = "{}"
			}
			a := inspectUnitInput(t, pair[0], input)
			b := inspectUnitInput(t, pair[1], input)
			if got, err := p.Compare(ctx, a, b, InputEnvironment{}); err != nil || got != 0 {
				t.Fatal(id, got, err)
			}
			for _, op := range []ComparisonOperator{CompareEqual, CompareNotEqual, CompareLess, CompareLessEqual, CompareGreater, CompareGreaterEqual} {
				if err := CheckComparisonOperator(op, pair[0], pair[1]); err != nil {
					t.Fatal(id, op, err)
				}
				if _, err := BindComparisonPredicate(op, pair[0], pair[1], types[Bool], CollationRef{}, false); err != nil {
					t.Fatal(id, op, err)
				}
			}
		}
		// Scalar and different-family operands remain invalid in either order,
		// including when a range/multirange is wrapped in nested domains.
		other := types[Int4Range]
		if id == Int4Range {
			other = types[Int8Range]
		}
		for _, left := range []*Descriptor{d, domain, nested} {
			for _, right := range []*Descriptor{types[Int4], types[Text], other} {
				for _, pair := range [][2]*Descriptor{{left, right}, {right, left}} {
					if _, err := BindComparison(pair[0], pair[1], CollationRef{}, false); err == nil {
						t.Fatal("incompatible comparison admitted", id, pair[0].Name(), pair[1].Name())
					}
					for op := CompareEqual; op <= CompareGreaterEqual; op++ {
						if _, err := BindComparisonPredicate(op, pair[0], pair[1], types[Bool], CollationRef{}, false); err == nil {
							t.Fatal("incompatible predicate admitted", id, op, pair[0].Name(), pair[1].Name())
						}
					}
				}
			}
		}
		// Scalar comparison must not accidentally enable an ORDER BY provider.
		if _, err := BindOrdering(d.Usage()); err == nil {
			t.Fatal("range ordering unexpectedly admitted", id)
		}
	}
}
