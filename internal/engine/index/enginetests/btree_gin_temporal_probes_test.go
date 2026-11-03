package enginetests_test

import (
	"context"
	"errors"
	"math"
	"slices"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestBtreeGINTemporalTokens(t *testing.T) {
	e, _, root := temporalEngine(t, false)
	for col, tc := range temporalPrivateCases {
		binding := root.Gin.Columns[col].Binding
		if tc.typ == "interval" {
			boolean, _ := e.CatalogRead().LookupType(sqltype.Bool)
			temporalIntervalComponents(t, binding, boolean)
		}
		var previous sqltype.GinToken
		for i, key := range temporalPrivateKeys(tc) {
			value := ginInput(t, binding.Usage.Type, key.text)
			var tokens []sqltype.GinToken
			if err := binding.Extract(context.Background(), value, func(token sqltype.GinToken) error { tokens = append(tokens, token); return nil }); err != nil {
				t.Fatal(err)
			}
			if len(tokens) != 1 || tokens[0].Kind != sqltype.GinOrdinary || !sqltype.SameDatum(tokens[0].Element, value) || tokens[0].Lexeme != "" {
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
		if tc.typ == "interval" || tc.typ == "timestamptz" || tc.typ == "timestamp" {
			pairs := [][2]string{{"2000-01-01", "2000-01-01 00:00+05"}}
			if tc.typ == "timestamptz" {
				pairs = [][2]string{{"2000-01-01 00:00+00", "2000-01-01 01:00+01"}}
			}
			if tc.typ == "interval" {
				pairs = [][2]string{{"1 mon", "30 days"}, {"1 mon", "720 hours"}, {"2147483647 months -2147483648 days", "2147483646 months -2147483618 days"}}
			}
			for _, pair := range pairs {
				a, b := sqltype.GinToken{Element: ginInput(t, binding.Usage.Type, pair[0])}, sqltype.GinToken{Element: ginInput(t, binding.Usage.Type, pair[1])}
				if cmp, err := binding.Compare(context.Background(), a, b, sqltype.InputEnvironment{}); err != nil || cmp != 0 {
					t.Fatal("temporal typed equivalence", cmp, err)
				}
			}
		}
	}
}
func TestBtreeGINTemporalPostings(t *testing.T) {
	_, _, _, store, root := temporalPhysical(t)
	before, entries := ginCopyPayload(root.Gin), gistCopyEntries(root)
	for _, entry := range root.Entries {
		for col, value := range entry.Keys {
			if !sqltype.SameDatum(value, store.Versions[entry.Position].Values[col+1]) {
				t.Fatal("dictionary equivalence changed source representation", entry.Position, col)
			}
		}
	}
	for col, tc := range temporalPrivateCases {
		column := root.Gin.Columns[col]
		keys := temporalPrivateKeys(tc)
		if len(column.Tokens) != len(keys)+1 {
			t.Fatal("distinct typed keys plus NULL", tc.typ, len(column.Tokens))
		}
		var all, nulls []int
		texts := temporalPrivateTexts(tc)
		for pos := range temporalPhysicalRows {
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
			selected, err := index.GinScalarCandidates(context.Background(), root.Gin, col, token.Element, 3, sqltype.InputEnvironment{})
			if err != nil || !slices.Equal(selected, got) || &selected[0] != &got[0] {
				t.Fatal("exact equality candidates", tc.typ, key.text, selected, err)
			}
		}
		if !slices.Equal(ginLookup(t, column, sqltype.GinToken{Kind: sqltype.GinNullItem}), compoundExactPostings(nulls)) {
			t.Fatal("NULL physical postings", tc.typ)
		}
		column.NonNull = []storage.GinPosting{{Row: 9999, Version: 9999, Position: 9999}}
		probe := &storage.GinRoot{Columns: []storage.GinColumn{column}}
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
			for i := range wants {
				wants[i] = slices.Sorted(slices.Values(wants[i]))
			}
			networkProbeProof(t, column, key.text, tc.absent, wants, all)
			for strategy, positions := range wants {
				positions = slices.Sorted(slices.Values(positions))
				got, err := index.GinScalarCandidates(context.Background(), probe, 0, ginInput(t, column.Binding.Usage.Type, key.text), strategy+1, sqltype.InputEnvironment{})
				if err != nil || !slices.Equal(got, compoundExactPostings(positions)) {
					t.Fatal("all typed endpoints", tc.typ, key.text, strategy+1, got, positions, err)
				}
			}
		}
		sameID := *column.Binding.Usage.Type
		if got, err := index.GinScalarCandidates(context.Background(), root.Gin, col, sqltype.Null(&sameID), 3, sqltype.InputEnvironment{}); err == nil || got != nil {
			t.Fatal("foreign generation probe", got, err)
		}
	}
	ginAssertOwners(t, root, store)
	if !scalarSameGin(root.Gin, before) || !scalarSameEntries(root.Entries, entries) {
		t.Fatal("probe mutated predecessor")
	}
}

func temporalIntervalComponents(t *testing.T, binding sqltype.GinBinding, boolean *sqltype.Descriptor) {
	t.Helper()
	finite, err := sqltype.BindDatetimeAgeFinite("isfinite", binding.Usage.Type, boolean, nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	texts := temporalPrivateTexts(temporalPrivateCases[5])
	for _, edge := range []struct {
		position     int
		months, days int32
		micros       int64
		finite       bool
	}{
		{0, math.MinInt32, math.MinInt32, math.MinInt64, false},
		{8, math.MinInt32, math.MinInt32, math.MinInt64 + 1, true},
		{9, 0, 0, math.MinInt64, true},
		{10, 0, 0, math.MaxInt64, true},
		{11, math.MaxInt32, math.MaxInt32, math.MaxInt64 - 1, true},
		{5, math.MaxInt32, math.MaxInt32, math.MaxInt64, false},
		{2, 1, 0, 0, true},
		{3, 0, 30, 0, true},
		{7, 0, 0, 2592000000000, true},
		{12, math.MaxInt32, math.MinInt32, 0, true},
		{13, math.MaxInt32 - 1, math.MinInt32 + 30, 0, true},
	} {
		value := ginInput(t, binding.Usage.Type, texts[edge.position])
		m, d, us, err := value.Interval()
		if err != nil || m != edge.months || d != edge.days || us != edge.micros {
			t.Fatal("interval source components", edge.position, m, d, us, err)
		}
		owned, err := sqltype.IntervalValue(binding.Usage.Type, edge.months, edge.days, edge.micros)
		if err != nil || !sqltype.SameDatum(value, owned) {
			t.Fatal("interval exact boundary ownership", edge.position, err)
		}
		result, err := finite.Evaluate(context.Background(), []sqltype.Value{value}, sqltype.InputEnvironment{})
		if err != nil {
			t.Fatal(err)
		}
		got, err := result.Boolean()
		if err != nil || got != edge.finite {
			t.Fatal("finite versus reserved sentinel", edge.position, got, err)
		}
	}
}
