package enginetests_test

import (
	"context"
	"errors"
	"slices"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
	"testing"
)

// These snapshots retain exact datum representations, not SQL equality:
// SameDatum checks concrete generation and signed-zero/NaN payload bits.
// Binding.Same retains the complete class/support/comparison capture, and
// postings and entries keep every row/version/physical-slot association.
func scalarSameGin(a, b *storage.GinRoot) bool {
	if a == nil || b == nil {
		return a == b
	}
	if (a.Columns == nil) != (b.Columns == nil) || len(a.Columns) != len(b.Columns) {
		return false
	}
	for col, left := range a.Columns {
		right := b.Columns[col]
		if !left.Binding.Same(right.Binding) || (left.NonNull == nil) != (right.NonNull == nil) || !slices.Equal(left.NonNull, right.NonNull) || (left.Tokens == nil) != (right.Tokens == nil) || len(left.Tokens) != len(right.Tokens) {
			return false
		}
		for ordinal, x := range left.Tokens {
			y := right.Tokens[ordinal]
			if x.Token.Kind != y.Token.Kind || x.Token.Lexeme != y.Token.Lexeme || (x.Postings == nil) != (y.Postings == nil) || !slices.Equal(x.Postings, y.Postings) {
				return false
			}
			if x.Token.Kind == sqltype.GinOrdinary {
				if !sqltype.SameDatum(x.Token.Element, y.Token.Element) {
					return false
				}
			} else if x.Token.Element != y.Token.Element {
				return false
			}
		}
	}
	return true
}

func scalarSameEntries(a, b []storage.IndexEntry) bool {
	if (a == nil) != (b == nil) || len(a) != len(b) {
		return false
	}
	for i, x := range a {
		y := b[i]
		if x.Row != y.Row || x.Version != y.Version || x.Position != y.Position || (x.Keys == nil) != (y.Keys == nil) || !slices.EqualFunc(x.Keys, y.Keys, sqltype.SameDatum) {
			return false
		}
	}
	return true
}

func TestBtreeGINScalarTokens(t *testing.T) {
	_, _, root := scalarEngine(t, false)
	for col, tc := range scalarPrivateCases {
		binding := root.Gin.Columns[col].Binding
		var previous sqltype.GinToken
		for i, key := range scalarPrivateKeys(tc) {
			value := ginInput(t, binding.Usage.Type, key.text)
			var tokens []sqltype.GinToken
			if err := binding.Extract(context.Background(), value, func(token sqltype.GinToken) error { tokens = append(tokens, token); return nil }); err != nil {
				t.Fatal(err)
			}
			if len(tokens) != 1 || tokens[0].Kind != sqltype.GinOrdinary || tokens[0].Element.Type() != value.Type() || tokens[0].Lexeme != "" {
				t.Fatal("not one immutable whole typed scalar", tc.typ, tokens)
			}
			if i > 0 {
				cmp, err := binding.Compare(context.Background(), previous, tokens[0], sqltype.InputEnvironment{})
				if err != nil || cmp >= 0 {
					t.Fatal("scalar dictionary order", tc.typ, key.text, cmp, err)
				}
			}
			previous = tokens[0]
		}
		var null []sqltype.GinToken
		if err := binding.Extract(context.Background(), sqltype.Null(binding.Usage.Type), func(token sqltype.GinToken) error { null = append(null, token); return nil }); err != nil || len(null) != 1 || null[0].Kind != sqltype.GinNullItem {
			t.Fatal("NULL placeholder", null, err)
		}
		foreign := *binding.Usage.Type
		for _, value := range []sqltype.Value{sqltype.Null(&foreign), ginInput(t, &foreign, tc.boundary)} {
			if err := binding.Extract(context.Background(), value, func(sqltype.GinToken) error { t.Fatal("foreign generation emitted token"); return nil }); err == nil {
				t.Fatal("equal-ID foreign generation accepted")
			}
		}
		if _, err := binding.Compare(context.Background(), previous, sqltype.GinToken{Element: ginInput(t, &foreign, tc.boundary)}, sqltype.InputEnvironment{}); err == nil {
			t.Fatal("foreign comparison accepted")
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := binding.Extract(ctx, sqltype.Null(binding.Usage.Type), func(sqltype.GinToken) error { t.Fatal("canceled extraction emitted"); return nil }); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		if _, err := binding.Compare(ctx, previous, previous, sqltype.InputEnvironment{}); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		if tc.typ == "float4" || tc.typ == "float8" {
			for _, pair := range [][2]string{{"-0", "0"}, {"NaN", "NaN"}} {
				a, b := sqltype.GinToken{Element: ginInput(t, binding.Usage.Type, pair[0])}, sqltype.GinToken{Element: ginInput(t, binding.Usage.Type, pair[1])}
				if cmp, err := binding.Compare(context.Background(), a, b, sqltype.InputEnvironment{}); err != nil || cmp != 0 {
					t.Fatal("zero/NaN typed equivalence", cmp, err)
				}
			}
		}
	}
}
func scalarZeroPositions(tc networkPrivateCase) [][]int {
	switch tc.typ {
	case "bool":
		return [][]int{nil, {0, 1}, {0, 1}, {0, 1, 2}, {2}}
	case "int8":
		return [][]int{{0, 1}, {0, 1, 2, 3}, {2, 3}, {2, 3, 4, 5, 7, 8, 9}, {4, 5, 7, 8, 9}}
	case "float4":
		return [][]int{{0, 1, 12}, {0, 1, 2, 3, 12}, {2, 3}, {2, 3, 4, 5, 6, 7, 8, 10, 11, 13, 14, 15, 16}, {4, 5, 6, 7, 8, 10, 11, 13, 14, 15, 16}}
	case "float8":
		return [][]int{{0, 1, 12}, {0, 1, 2, 3, 12}, {2, 3}, {2, 3, 4, 5, 6, 7, 8, 10, 11, 13}, {4, 5, 6, 7, 8, 10, 11, 13}}
	default:
		return [][]int{{0, 1}, {0, 1, 2, 3}, {2, 3}, {2, 3, 4, 5}, {4, 5}}
	}
}
func TestBtreeGINScalarPostings(t *testing.T) {
	_, _, _, store, root := scalarPhysical(t)
	before, entries := ginCopyPayload(root.Gin), gistCopyEntries(root)
	for col, tc := range scalarPrivateCases {
		column := root.Gin.Columns[col]
		keys := scalarPrivateKeys(tc)
		if len(column.Tokens) != len(keys)+1 {
			t.Fatal("distinct typed keys plus NULL", tc.typ, len(column.Tokens))
		}
		var all, nulls []int
		texts := scalarPrivateTexts(tc)
		for pos := range 17 {
			if pos < len(texts) && texts[pos] != "" {
				all = append(all, pos)
			} else {
				nulls = append(nulls, pos)
			}
		}
		for ordinal, key := range keys {
			token := sqltype.GinToken{Element: ginInput(t, column.Binding.Usage.Type, key.text)}
			cmp, err := column.Binding.Compare(context.Background(), column.Tokens[ordinal].Token, token, sqltype.InputEnvironment{})
			got := ginLookup(t, column, token)
			if err != nil || cmp != 0 || !slices.Equal(got, compoundExactPostings(key.positions)) || !slices.Equal(got, column.Tokens[ordinal].Postings) {
				t.Fatal("physical typed dictionary", tc.typ, key.text, got, err)
			}
			if &got[0] != &column.Tokens[ordinal].Postings[0] {
				t.Fatal("equality lost backing")
			}
			selected, err := index.GinScalarCandidates(context.Background(), column, token.Element, 3, sqltype.InputEnvironment{})
			if err != nil || !slices.Equal(selected, got) || &selected[0] != &got[0] {
				t.Fatal("exact equality candidates", tc.typ, key.text, selected, err)
			}
		}
		if !slices.Equal(ginLookup(t, column, sqltype.GinToken{Kind: sqltype.GinNullItem}), compoundExactPostings(nulls)) {
			t.Fatal("NULL physical postings", tc.typ)
		}
		column.NonNull = []storage.GinPosting{{Row: 9999, Version: 9999, Position: 9999}}
		networkProbeProof(t, column, tc.boundary, tc.absent, scalarZeroPositions(tc), all)
		// Every finite/special dictionary boundary exercises all five strategies;
		// the independent sorted key groups specify exact physical memberships.
		for ordinal, key := range keys {
			var lower, equal, upper []int
			for i, k := range keys {
				switch {
				case i < ordinal:
					lower = append(lower, k.positions...)
				case i == ordinal:
					equal = append(equal, k.positions...)
				default:
					upper = append(upper, k.positions...)
				}
			}
			wants := [][]int{lower, append(slices.Clone(lower), equal...), equal, append(slices.Clone(equal), upper...), upper}
			for strategy, positions := range wants {
				positions = slices.Sorted(slices.Values(positions))
				got, err := index.GinScalarCandidates(context.Background(), column, ginInput(t, column.Binding.Usage.Type, key.text), strategy+1, sqltype.InputEnvironment{})
				if err != nil || !slices.Equal(got, compoundExactPostings(positions)) {
					t.Fatal("all typed endpoints", tc.typ, key.text, strategy+1, got, positions, err)
				}
			}
		}
		sameID := *column.Binding.Usage.Type
		if got, err := index.GinScalarCandidates(context.Background(), column, sqltype.Null(&sameID), 3, sqltype.InputEnvironment{}); err == nil || got != nil {
			t.Fatal("foreign generation probe", got, err)
		}
	}
	ginAssertOwners(t, root, store)
	if !scalarSameGin(root.Gin, before) || !scalarSameEntries(root.Entries, entries) {
		t.Fatal("probe mutated predecessor")
	}
}
