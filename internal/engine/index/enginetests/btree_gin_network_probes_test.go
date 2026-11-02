package enginetests_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

func TestBtreeGINNetworkTokens(t *testing.T) {
	_, _, root := networkEngine(t, false)
	for col, tc := range networkPrivateCases {
		binding := root.Gin.Columns[col].Binding
		texts := []string{tc.lower, tc.boundary, tc.upper}
		switch tc.typ {
		case "inet":
			texts = []string{"0.0.0.0/0", "10.0.0.0/16", "10.0.0.0/24", "10.0.0.1/24", "255.255.255.255/32", "::/0", "::/128"}
		case "cidr":
			texts = []string{"0.0.0.0/0", "10.0.0.0/16", "10.0.0.0/24", "10.0.1.0/24", "255.255.255.255/32", "::/0", "::/128"}
		case "uuid":
			texts = []string{tc.lower, "00000001-0000-0000-0000-000000000000", "00000100-0000-0000-0000-000000000000", tc.boundary, tc.upper}
		}
		var previous sqltype.GinToken
		for i, text := range texts {
			value := ginInput(t, binding.Usage.Type, text)
			var tokens []sqltype.GinToken
			if err := binding.Extract(context.Background(), value, func(token sqltype.GinToken) error { tokens = append(tokens, token); return nil }); err != nil {
				t.Fatal(err)
			}
			if len(tokens) != 1 || tokens[0].Kind != sqltype.GinOrdinary || tokens[0].Element.Type() != value.Type() || tokens[0].Lexeme != "" {
				t.Fatal("whole immutable source token", tc.typ, tokens)
			}
			if i > 0 {
				cmp, err := binding.Compare(context.Background(), previous, tokens[0], sqltype.InputEnvironment{})
				if err != nil || cmp >= 0 {
					t.Fatal("typed unsigned/prefix/family/UUID byte ordering", tc.typ, text, cmp, err)
				}
			}
			previous = tokens[0]
		}
		var null []sqltype.GinToken
		if err := binding.Extract(context.Background(), sqltype.Null(binding.Usage.Type), func(token sqltype.GinToken) error { null = append(null, token); return nil }); err != nil || len(null) != 1 || null[0].Kind != sqltype.GinNullItem {
			t.Fatal("NULL category", tc.typ, null, err)
		}
		foreign := root.Gin.Columns[(col+1)%6].Binding.Usage.Type
		if err := binding.Extract(context.Background(), sqltype.Null(foreign), func(sqltype.GinToken) error { t.Fatal("foreign extraction emitted token"); return nil }); err == nil {
			t.Fatal("foreign source admitted")
		}
		sameID := *binding.Usage.Type
		if err := binding.Extract(context.Background(), sqltype.Null(&sameID), func(sqltype.GinToken) error { t.Fatal("equal-ID foreign generation emitted token"); return nil }); err == nil {
			t.Fatal("equal-ID foreign generation admitted")
		}
		if _, err := binding.Compare(context.Background(), previous, sqltype.GinToken{Element: ginInput(t, &sameID, texts[len(texts)-1])}, sqltype.InputEnvironment{}); err == nil {
			t.Fatal("foreign token comparison admitted")
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := binding.Extract(ctx, sqltype.Null(binding.Usage.Type), func(sqltype.GinToken) error { t.Fatal("canceled extraction emitted token"); return nil }); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		if _, err := binding.Compare(ctx, previous, previous, sqltype.InputEnvironment{}); !errors.Is(err, context.Canceled) {
			t.Fatal("comparison cancellation", err)
		}
	}
}

func TestBtreeGINNetworkPostings(t *testing.T) {
	_, _, _, store, root := networkPhysical(t)
	before, entries := ginCopyPayload(root.Gin), gistCopyEntries(root)
	for col, tc := range networkPrivateCases {
		column := root.Gin.Columns[col]
		if len(column.Tokens) != 4 {
			t.Fatal("three ordinary keys and one NULL", tc.typ, len(column.Tokens))
		}
		for ordinal, item := range []struct {
			text      string
			positions []int
		}{{tc.lower, []int{0}}, {tc.boundary, []int{1, 2}}, {tc.upper, []int{3}}} {
			token := sqltype.GinToken{Element: ginInput(t, column.Binding.Usage.Type, item.text)}
			cmp, err := column.Binding.Compare(context.Background(), column.Tokens[ordinal].Token, token, sqltype.InputEnvironment{})
			got := ginLookup(t, column, token)
			if err != nil || cmp != 0 || !slices.Equal(got, compoundExactPostings(item.positions)) || !slices.Equal(column.Tokens[ordinal].Postings, got) {
				t.Fatal("exact physical dictionary", tc.typ, ordinal, got, err)
			}
			if &got[0] != &column.Tokens[ordinal].Postings[0] {
				t.Fatal("lookup copied posting backing")
			}
		}
		if !slices.Equal(ginLookup(t, column, sqltype.GinToken{Kind: sqltype.GinNullItem}), compoundExactPostings([]int{4})) {
			t.Fatal("NULL physical owner")
		}
		column.NonNull = []storage.GinPosting{{Row: 9999, Version: 9999, Position: 9999}}
		networkProbeProof(t, column, tc.boundary, tc.absent, [][]int{{0}, {0, 1, 2}, {1, 2}, {1, 2, 3}, {3}}, []int{0, 1, 2, 3})
		sameID := *column.Binding.Usage.Type
		if got, err := index.GinScalarCandidates(context.Background(), column, sqltype.Null(&sameID), 3, sqltype.InputEnvironment{}); err == nil || got != nil {
			t.Fatal("equal-ID foreign scalar probe admitted", got, err)
		}
	}
	ginAssertOwners(t, root, store)
	if !reflect.DeepEqual(root.Gin, before) || !reflect.DeepEqual(root.Entries, entries) {
		t.Fatal("probe mutated immutable graph")
	}
	t.Run("selected_oid_int4", networkIntegerPostings)
}

func networkProbeProof(t *testing.T, column storage.GinColumn, boundary, absent string, want [][]int, all []int) {
	t.Helper()
	query := ginInput(t, column.Binding.Usage.Type, boundary)
	for i, positions := range want {
		got, err := index.GinScalarCandidates(context.Background(), column, query, i+1, sqltype.InputEnvironment{})
		if err != nil || !slices.Equal(got, compoundExactPostings(positions)) {
			t.Fatal("selected strategy exact triples", i+1, got, positions, err)
		}
		if i == 2 {
			dictionary := ginLookup(t, column, sqltype.GinToken{Element: query})
			if &got[0] != &dictionary[0] {
				t.Fatal("equality copied posting backing")
			}
		}
		null, err := index.GinScalarCandidates(context.Background(), column, sqltype.Null(query.Type()), i+1, sqltype.InputEnvironment{})
		if err != nil || len(null) != 0 {
			t.Fatal("strict NULL", null, err)
		}
		empty := column
		empty.Tokens = nil
		got, err = index.GinScalarCandidates(context.Background(), empty, query, i+1, sqltype.InputEnvironment{})
		if err != nil || len(got) != 0 {
			t.Fatal("empty dictionary", got, err)
		}
	}
	if absent != "" {
		if got, err := index.GinScalarCandidates(context.Background(), column, ginInput(t, query.Type(), absent), 3, sqltype.InputEnvironment{}); err != nil || len(got) != 0 {
			t.Fatal("missing equality", got, err)
		}
	}
	for _, strategy := range []int{0, 6, 15} {
		for _, value := range []sqltype.Value{query, sqltype.Null(query.Type())} {
			got, err := index.GinScalarCandidates(context.Background(), column, value, strategy, sqltype.InputEnvironment{})
			if err == nil || got != nil {
				t.Fatal("invented strategy", strategy, got, err)
			}
		}
	}
	first, last := column.Tokens[0].Token.Element, column.Tokens[len(column.Tokens)-2].Token.Element
	for _, edge := range []struct {
		value     sqltype.Value
		strategy  int
		positions []int
	}{{first, 1, nil}, {last, 5, nil}, {first, 4, all}, {last, 2, all}} {
		got, err := index.GinScalarCandidates(context.Background(), column, edge.value, edge.strategy, sqltype.InputEnvironment{})
		if err != nil || !slices.Equal(got, compoundExactPostings(edge.positions)) {
			t.Fatal("endpoint range", got, err)
		}
	}
	categories := column
	categories.Tokens = slices.Clone(column.Tokens[:len(column.Tokens)-1])
	for _, kind := range []sqltype.GinTokenKind{sqltype.GinNullElement, sqltype.GinEmptyItem, sqltype.GinNullItem} {
		categories.Tokens = append(categories.Tokens, storage.GinPostingList{Token: sqltype.GinToken{Kind: kind}, Postings: []storage.GinPosting{{Row: 9999, Version: 9999, Position: 9999}}})
	}
	got, err := index.GinScalarCandidates(context.Background(), categories, first, 4, sqltype.InputEnvironment{})
	if err != nil || !slices.Equal(got, compoundExactPostings(all)) {
		t.Fatal("placeholder category leaked", got, err)
	}
	interrupted := &spgistInterruptContext{Context: context.Background(), remaining: 3}
	got, err = index.GinScalarCandidates(interrupted, column, query, 4, sqltype.InputEnvironment{})
	if !errors.Is(err, context.Canceled) || got != nil {
		t.Fatal("partial canceled candidates", got, err)
	}
}
func networkIntegerPostings(t *testing.T) {
	e, _, _ := networkEngine(t, false)
	enginetest.Run(t, e, "CREATE TABLE network_unsigned(v integer)")
	enginetest.Run(t, e, "CREATE INDEX network_unsigned_i ON network_unsigned USING gin(v oid_ops)")
	// Build the physical fixture with independently assigned row/version/slot IDs.
	idx, _ := ginInstalledRoot(t, e, "network_unsigned_i")
	view := e.CatalogRead()
	parent := view.MustRelation(idx.SQLIndex.Parent)
	typ := parent.Columns[0].Usage.Type
	store := &storage.Relation{}
	for i, text := range []string{"0", "2147483647", "-2147483648", "-1", ""} {
		v := sqltype.Null(typ)
		if i != 4 {
			v = ginInput(t, typ, text)
		}
		store.Versions = append(store.Versions, storage.RowVersion{Row: ids.RowID(101 + i), Version: ids.VersionID(201 + i), Born: 1, Values: []sqltype.Value{v}})
	}
	root := networkBuild(t, view, parent, idx, store)
	for ordinal, text := range []string{"0", "2147483647", "-2147483648", "-1"} {
		token := sqltype.GinToken{Element: ginInput(t, typ, text)}
		cmp, err := root.Gin.Columns[0].Binding.Compare(context.Background(), root.Gin.Columns[0].Tokens[ordinal].Token, token, sqltype.InputEnvironment{})
		if err != nil || cmp != 0 || !slices.Equal(root.Gin.Columns[0].Tokens[ordinal].Postings, compoundExactPostings([]int{ordinal})) {
			t.Fatal("oid_ops must reinterpret INT4 unsigned", ordinal, cmp, err)
		}
	}
	networkProbeProof(t, root.Gin.Columns[0], "-2147483648", "1", [][]int{{0, 1}, {0, 1, 2}, {2}, {2, 3}, {3}}, []int{0, 1, 2, 3})
	ginAssertOwners(t, root, store)
}
