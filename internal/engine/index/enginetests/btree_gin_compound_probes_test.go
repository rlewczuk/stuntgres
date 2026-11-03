package enginetests_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

func compoundExactPostings(positions []int) []storage.GinPosting {
	var out []storage.GinPosting
	for _, pos := range positions {
		out = append(out, storage.GinPosting{Row: ids.RowID(101 + pos), Version: ids.VersionID(201 + pos), Position: pos})
	}
	return out
}

func compoundScalarProbeProof(t *testing.T, root *storage.IndexRoot) {
	t.Helper()
	before, entries := ginCopyPayload(root.Gin), gistCopyEntries(root)
	for col, tc := range []struct {
		boundary, absent string
		all              []int
		want             [][]int
	}{
		{"100", "001", []int{0, 1, 2, 3, 4}, [][]int{{0, 1}, {0, 1, 2, 3}, {2, 3}, {2, 3, 4}, {4}}},
		{"0", "000", []int{0, 1, 2, 3, 4, 5}, [][]int{{0}, {0, 1}, {1}, {1, 2, 3, 4, 5}, {2, 3, 4, 5}}},
		{"a", "", []int{0, 1, 2, 3, 4}, [][]int{{0, 1}, {0, 1, 2, 3}, {2, 3}, {2, 3, 4}, {4}}},
		{"0.01", "0.02", []int{0, 1, 2, 3, 4, 5}, [][]int{{0, 1, 2}, {0, 1, 2, 3, 4}, {3, 4}, {3, 4, 5}, {5}}},
	} {
		column := root.Gin.Columns[col]
		query := ginInput(t, column.Binding.Usage.Type, tc.boundary)
		// NonNull is deliberately unusable as a candidate fallback: only the real
		// dictionary lists carry the exact independently assigned physical triples.
		column.NonNull = []storage.GinPosting{{Row: 9999, Version: 9999, Position: 9999}}
		probe := &storage.GinRoot{Columns: []storage.GinColumn{column}}
		for i, positions := range tc.want {
			got, err := index.GinScalarCandidates(context.Background(), probe, 0, query, i+1, sqltype.InputEnvironment{})
			exact := compoundExactPostings(positions)
			if err != nil || !slices.Equal(got, exact) {
				t.Fatal("selective scalar strategy triples", col, i+1, got, exact, err)
			}
			if i == 2 && len(got) > 0 {
				dictionary := ginLookup(t, column, sqltype.GinToken{Element: query})
				if &got[0] != &dictionary[0] {
					t.Fatal("equality copied or replaced exact posting-list backing", col)
				}
			}
			null, err := index.GinScalarCandidates(context.Background(), probe, 0, sqltype.Null(column.Binding.Usage.Type), i+1, sqltype.InputEnvironment{})
			if err != nil || len(null) != 0 {
				t.Fatal("strict scalar NULL probe", col, i+1, null, err)
			}
			empty := column
			empty.Tokens = nil
			got, err = index.GinScalarCandidates(context.Background(), &storage.GinRoot{Columns: []storage.GinColumn{empty}}, 0, query, i+1, sqltype.InputEnvironment{})
			if err != nil || len(got) != 0 {
				t.Fatal("empty dictionary fabricated candidates", col, i+1, got, err)
			}
		}
		if tc.absent != "" {
			absent := ginInput(t, column.Binding.Usage.Type, tc.absent)
			got, err := index.GinScalarCandidates(context.Background(), probe, 0, absent, 3, sqltype.InputEnvironment{})
			if err != nil || len(got) != 0 {
				t.Fatal("absent equality returned heap/NonNull members", col, got, err)
			}
		}
		for _, strategy := range []int{0, 6, 15} {
			for _, value := range []sqltype.Value{query, sqltype.Null(query.Type())} {
				got, err := index.GinScalarCandidates(context.Background(), probe, 0, value, strategy, sqltype.InputEnvironment{})
				if err == nil || got != nil {
					t.Fatal("invalid scalar strategy accepted", col, strategy, value, got, err)
				}
			}
		}
		first := column.Tokens[0].Token.Element
		lastOrdinary := len(column.Tokens) - 2
		last := column.Tokens[lastOrdinary].Token.Element
		for _, edge := range []struct {
			query     sqltype.Value
			strategy  int
			positions []int
		}{
			{first, 1, nil}, {first, 4, tc.all}, {last, 5, nil}, {last, 2, tc.all},
		} {
			got, err := index.GinScalarCandidates(context.Background(), probe, 0, edge.query, edge.strategy, sqltype.InputEnvironment{})
			if err != nil || !slices.Equal(got, compoundExactPostings(edge.positions)) {
				t.Fatal("exact endpoint scalar range", col, edge.strategy, got, err)
			}
		}
		// Real NULL placeholders and adversarial other nonordinary categories can
		// never leak into even a whole ordinary-key range.
		ordinary := slices.Clone(column.Tokens[:lastOrdinary+1])
		for _, kind := range []sqltype.GinTokenKind{sqltype.GinNullElement, sqltype.GinEmptyItem, sqltype.GinNullItem} {
			ordinary = append(ordinary, storage.GinPostingList{Token: sqltype.GinToken{Kind: kind}, Postings: []storage.GinPosting{{Row: 9999, Version: 9999, Position: 9999}}})
		}
		categories := column
		categories.Tokens = ordinary
		got, err := index.GinScalarCandidates(context.Background(), &storage.GinRoot{Columns: []storage.GinColumn{categories}}, 0, first, 4, sqltype.InputEnvironment{})
		if err != nil || !slices.Equal(got, compoundExactPostings(tc.all)) {
			t.Fatal("nonordinary category leaked into scalar range", col, got, err)
		}
		interrupted := &spgistInterruptContext{Context: context.Background(), remaining: 3}
		got, err = index.GinScalarCandidates(interrupted, probe, 0, query, 4, sqltype.InputEnvironment{})
		if !errors.Is(err, context.Canceled) || got != nil {
			t.Fatal("interrupted scalar probe exposed partial candidates", col, got, err)
		}
	}
	for col := range 4 {
		foreign := root.Gin.Columns[(col+1)%4].Binding.Usage.Type
		if got, err := index.GinScalarCandidates(context.Background(), root.Gin, col, sqltype.Null(foreign), 3, sqltype.InputEnvironment{}); err == nil || got != nil {
			t.Fatal("foreign scalar query generation admitted", col, got, err)
		}
	}
	for col := 4; col < 6; col++ {
		column := root.Gin.Columns[col]
		if got, err := index.GinScalarCandidates(context.Background(), root.Gin, col, sqltype.Null(column.Binding.Usage.Type), 3, sqltype.InputEnvironment{}); err == nil || got != nil {
			t.Fatal("array/vector admitted as scalar", col, got, err)
		}
	}
	if !reflect.DeepEqual(root.Gin, before) || !reflect.DeepEqual(root.Entries, entries) {
		t.Fatal("scalar probes mutated retained root/dictionary/postings")
	}
}

func compoundScalarMergeProof(t *testing.T, view catalog.Read, parent, idx *catalog.Relation, store *storage.Relation, root *storage.IndexRoot) {
	t.Helper()
	before, entries := ginCopyPayload(root.Gin), gistCopyEntries(root)
	evaluation := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Catalog: view}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
	row := storage.RowVersion{Row: 199, Version: 299, Values: slices.Clone(store.Versions[4].Values), IndexOutputs: map[ids.ObjectAddress]storage.IndexOutput{idx.Address(): {Index: idx, Member: true, Keys: root.Entries[4].Keys}}}
	delta := &index.RowPublication{Store: store, Root: store.Versions, SingleSlot: -1, Writes: []storage.RowVersion{row}}
	candidate, err := index.MergeGinIndexRoot(evaluation, parent, idx, root, delta, true, false)
	if err != nil || candidate != nil {
		t.Fatal("scalar nonmaterializing admission produced installable candidate", candidate, err)
	}
	candidate, err = index.MergeGinIndexRoot(evaluation, parent, idx, root, delta, true, true)
	if err != nil || candidate == nil {
		t.Fatal("valid scalar delta lacked materialized control", candidate, err)
	}
	query := ginInput(t, root.Gin.Columns[0].Binding.Usage.Type, "111")
	got, err := index.GinScalarCandidates(context.Background(), candidate.Gin, 0, query, 3, sqltype.InputEnvironment{})
	want := []storage.GinPosting{{Row: 105, Version: 205, Position: 4}, {Row: 199, Version: 299, Position: 7}}
	if err != nil || !slices.Equal(got, want) {
		t.Fatal("scalar delta lost produced physical ownership", got, want, err)
	}
	for _, materialize := range []bool{false, true} {
		eval := *evaluation
		eval.Context = &spgistInterruptContext{Context: context.Background(), remaining: 3}
		candidate, err = index.MergeGinIndexRoot(&eval, parent, idx, root, delta, true, materialize)
		if !errors.Is(err, context.Canceled) || candidate != nil {
			t.Fatal("scalar merge interruption exposed candidate", materialize, candidate, err)
		}
	}
	if !reflect.DeepEqual(root.Gin, before) || !reflect.DeepEqual(root.Entries, entries) {
		t.Fatal("scalar delta admission mutated predecessor")
	}
}

func compoundScalarLookup(t *testing.T, root *storage.GinRoot, ordinal int, value sqltype.Value) []storage.GinPosting {
	t.Helper()
	postings, err := index.GinScalarCandidates(context.Background(), root, ordinal, value, 3, sqltype.InputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	return postings
}
