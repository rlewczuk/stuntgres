package enginetests_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strconv"
	"testing"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
	"stuntgres/internal/util"
)

func TestBtreeGINCompoundRangeResources(t *testing.T) {
	var smallBytes [4]int64
	for _, count := range []int{256, 2048} {
		t.Run(strconv.Itoa(count), func(t *testing.T) {
			e, idx, _ := compoundEngine(t, false)
			view := e.CatalogRead()
			parent := view.MustRelation(idx.SQLIndex.Parent)
			integer, _ := view.LookupType(sqltype.Int4)
			store := &storage.Relation{Versions: make([]storage.RowVersion, 0, count+1)}
			for i := range count {
				id, err := sqltype.Integer(integer, int64(i+1))
				if err != nil {
					t.Fatal(err)
				}
				values := []sqltype.Value{id, ginInput(t, parent.Columns[1].Usage.Type, "000"), ginInput(t, parent.Columns[2].Usage.Type, ""), ginInput(t, parent.Columns[3].Usage.Type, "z"), ginInput(t, parent.Columns[4].Usage.Type, strconv.Itoa(i)), ginInput(t, parent.Columns[5].Usage.Type, "{2}"), ginInput(t, parent.Columns[6].Usage.Type, "cat:1")}
				// Distinct dictionary values run in the reverse of physical posting order.
				// Consecutive ordered triples also share a Row but have distinct Versions.
				ordinal := count - 1 - i
				store.Versions = append(store.Versions, storage.RowVersion{Row: ids.RowID(1000 + ordinal/2), Version: ids.VersionID(2000 + ordinal), Born: 1, Values: values})
			}
			nullID, _ := sqltype.Integer(integer, int64(count+1))
			nulls := []sqltype.Value{nullID}
			for _, column := range parent.Columns[1:] {
				nulls = append(nulls, sqltype.Null(column.Usage.Type))
			}
			store.Versions = append(store.Versions, storage.RowVersion{Row: ids.RowID(1000 + count), Version: ids.VersionID(2000 + count), Born: 1, Values: nulls})
			evaluation := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Catalog: view}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
			root, err := index.BuildIndexRoot(evaluation, parent, idx.SQLIndex, store, nil)
			if err != nil {
				t.Fatal(err)
			}
			ginAssertOwners(t, root, store)
			source, entries := ginCopyPayload(root.Gin), gistCopyEntries(root)
			column := root.Gin.Columns[3]
			if !column.Binding.IsScalar() || len(column.Tokens) != count+1 || column.Tokens[count].Token.Kind != sqltype.GinNullItem {
				t.Fatal("resource fixture lacks real ordinary dictionary and separate NULL item")
			}
			for _, list := range column.Tokens[:count] {
				if list.Token.Kind != sqltype.GinOrdinary || len(list.Postings) != 1 {
					t.Fatal("resource fixture did not produce distinct singleton scalar lists")
				}
			}
			expected := make([]storage.GinPosting, count)
			for i := range count {
				expected[i] = storage.GinPosting{Row: ids.RowID(1000 + i/2), Version: ids.VersionID(2000 + i), Position: count - 1 - i}
			}
			// Poison the shortcut vector only on the copied probe column: the actual
			// source and its dictionaries remain genuine and immutable.
			column.NonNull = []storage.GinPosting{{Row: 999999, Version: 999999, Position: 999999}}
			probe := &storage.GinRoot{Columns: []storage.GinColumn{column}}
			cases := []struct {
				strategy int
				bound    string
			}{{1, strconv.Itoa(count)}, {2, strconv.Itoa(count - 1)}, {4, "0"}, {5, "-1"}}
			for caseIndex, tc := range cases {
				t.Run(strconv.Itoa(tc.strategy), func(t *testing.T) {
					query := ginInput(t, column.Binding.Usage.Type, tc.bound)
					var got []storage.GinPosting
					var probeErr error
					allocations, bytes := util.AllocationsPerCall(3, func() {
						got, probeErr = index.GinScalarCandidates(context.Background(), probe, 0, query, tc.strategy, sqltype.InputEnvironment{})
					})
					if probeErr != nil || !slices.Equal(got, expected) {
						t.Fatal("broad range lost exact Row/Version/Position order or included NULL/shortcut rows", tc.strategy, probeErr, got)
					}
					stopped, stop := context.WithCancel(context.Background())
					stop()
					if candidate, err := index.GinScalarCandidates(stopped, probe, 0, query, tc.strategy, sqltype.InputEnvironment{}); !errors.Is(err, context.Canceled) || candidate != nil {
						t.Fatal("pre-canceled range exposed postings", candidate, err)
					}
					if allocations > 8 || bytes > int64(192*count+8192) {
						t.Errorf("range copied growing prefixes: keys=%d strategy=%d allocations=%d bytes=%d; require <=8 allocations and <=%d bytes/call", count, tc.strategy, allocations, bytes, 192*count+8192)
					}
					if count == 256 {
						smallBytes[caseIndex] = bytes
					} else if bytes > 12*smallBytes[caseIndex]+8192 {
						t.Errorf("range allocation grew superlinearly: strategy=%d small=%d large=%d bytes/call", tc.strategy, smallBytes[caseIndex], bytes)
					}
					// Count ordinary successful cancellation polls, then cancel exactly at
					// the final one. This is deterministic, needs no goroutine and rejects a
					// nonnil partial/successful slice paired with context.Canceled.
					const allowance = 1 << 30
					counted := &spgistInterruptContext{Context: context.Background(), remaining: allowance}
					if _, err := index.GinScalarCandidates(counted, probe, 0, query, tc.strategy, sqltype.InputEnvironment{}); err != nil {
						t.Fatal(err)
					}
					polls := allowance - counted.remaining
					if polls < 2 {
						t.Fatal("range omitted admitted cancellation observations", polls)
					}
					merging := &spgistInterruptContext{Context: context.Background(), remaining: polls * 3 / 4}
					if candidate, err := index.GinScalarCandidates(merging, probe, 0, query, tc.strategy, sqltype.InputEnvironment{}); !errors.Is(err, context.Canceled) || candidate != nil {
						t.Errorf("admitted canceled merge exposed partial postings: strategy=%d count=%d error=%v", tc.strategy, len(candidate), err)
					}
					late := &spgistInterruptContext{Context: context.Background(), remaining: polls}
					canceled, err := index.GinScalarCandidates(late, probe, 0, query, tc.strategy, sqltype.InputEnvironment{})
					if !errors.Is(err, context.Canceled) || canceled != nil {
						t.Errorf("late canceled range exposed successful/partial postings: strategy=%d count=%d error=%v", tc.strategy, len(canceled), err)
					}
				})
			}
			equalityQuery := store.Versions[count/2].Values[4]
			matching, err := column.Lookup(context.Background(), sqltype.GinToken{Element: equalityQuery}, sqltype.InputEnvironment{})
			if err != nil || len(matching) != 1 {
				t.Fatal("missing genuine equality list", matching, err)
			}
			var equal []storage.GinPosting
			var equalErr error
			allocations, bytes := util.AllocationsPerCall(5, func() {
				equal, equalErr = index.GinScalarCandidates(context.Background(), probe, 0, equalityQuery, 3, sqltype.InputEnvironment{})
			})
			if equalErr != nil || len(equal) != 1 || &equal[0] != &matching[0] || allocations != 0 || bytes != 0 {
				t.Fatal("equality lost exact zero-allocation backing", equalErr, allocations, bytes)
			}
			if got, err := index.GinScalarCandidates(context.Background(), probe, 0, sqltype.Null(column.Binding.Usage.Type), 4, sqltype.InputEnvironment{}); err != nil || got != nil {
				t.Fatal("NULL query exposed ordinary/NULL postings", got, err)
			}
			minimum := ginInput(t, column.Binding.Usage.Type, "0")
			if got, err := index.GinScalarCandidates(context.Background(), probe, 0, minimum, 1, sqltype.InputEnvironment{}); err != nil || got != nil {
				t.Fatal("empty selected dictionary interval exposed postings", got, err)
			}
			singleton, err := index.GinScalarCandidates(context.Background(), probe, 0, minimum, 2, sqltype.InputEnvironment{})
			if err != nil || len(singleton) != 1 || &singleton[0] != &column.Tokens[0].Postings[0] {
				t.Fatal("single selected list lost borrowed immutable backing", singleton, err)
			}
			for _, strategy := range []int{6, 15} {
				if got, err := index.GinScalarCandidates(context.Background(), probe, 0, sqltype.Null(column.Binding.Usage.Type), strategy, sqltype.InputEnvironment{}); err == nil || got != nil {
					t.Fatal("NULL query authorized unavailable strategy", strategy, got, err)
				}
			}
			// A copied sorted dictionary edge repeats an exact physical triple across
			// two selected lists. Distinct Versions of one Row stay; only the exact
			// repeated Row/Version/Position must disappear from the union.
			duplicate := column
			duplicate.Tokens = slices.Clone(column.Tokens)
			duplicate.Tokens[1].Postings = append(slices.Clone(column.Tokens[1].Postings), column.Tokens[0].Postings[0])
			query := ginInput(t, column.Binding.Usage.Type, "0")
			if got, err := index.GinScalarCandidates(context.Background(), &storage.GinRoot{Columns: []storage.GinColumn{duplicate}}, 0, query, 4, sqltype.InputEnvironment{}); err != nil || !slices.Equal(got, expected) {
				t.Fatal("range did not union exact triples while retaining distinct versions", err, got)
			}
			if !reflect.DeepEqual(root.Gin, source) || !reflect.DeepEqual(root.Entries, entries) {
				t.Fatal("resource/equality/cancellation probes mutated real source root")
			}
		})
	}
}
