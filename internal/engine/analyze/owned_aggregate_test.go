package analyze

import (
	"testing"

	"stuntgres/internal/engine/catalog"
)

func TestAggregateOrderedFrozenCallShape(t *testing.T) {
	for _, tc := range []struct {
		name, kind, signature string
		declared int16
		variadic bool
		direct, rows int
		valid bool
	}{
		{"fixed", "o", "25,16,23", 2, false, 2, 1, true},
		{"fixed_wrong_direct", "o", "25,16,23", 2, false, 1, 2, false},
		{"zero_direct", "o", "23", 0, false, 0, 1, true},
		{"zero_direct_extra", "o", "23", 0, false, 1, 1, false},
		{"hypothetical_prefix", "h", "25,23,23", 2, false, 2, 1, true},
		{"paired_expanded", "h", "2276", 1, true, 3, 3, true},
		{"paired_mismatch", "h", "2276", 1, true, 2, 3, false},
		{"prefixed_expanded", "h", "25,2276", 2, true, 3, 2, true},
		{"prefixed_mismatch", "h", "25,2276", 2, true, 2, 2, false},
		{"ordered_variadic_minimum", "o", "2276", 1, true, 1, 2, true},
		{"ordered_variadic_missing", "o", "2276", 1, true, 0, 2, false},
		{"rows_only", "o", "2276", 0, true, 0, 3, true},
		{"rows_only_extra_direct", "o", "2276", 0, true, 1, 3, false},
		{"missing_order", "o", "23", 0, false, 0, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			declaration := &catalog.Procedure{Signature: catalog.ProcedureSignature(tc.signature), Aggregate: &catalog.OwnedAggregate{Variadic: tc.variadic, Definition: catalog.AggregateDefinition{Kind: tc.kind, DirectArgs: tc.declared}}}
			if got := OwnedAggregateDirectCountValid(declaration, tc.direct, tc.rows); got != tc.valid {
				t.Fatalf("expanded direct/row counts (%d,%d): got %t want %t", tc.direct, tc.rows, got, tc.valid)
			}
		})
	}
	if OwnedAggregateDirectCountValid(nil, 0, 1) {
		t.Fatal("missing declaration admitted")
	}
}
