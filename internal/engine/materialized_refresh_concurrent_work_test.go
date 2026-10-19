package engine

import (
	"context"
	"reflect"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/sqltype"
	"testing"
)

// Count real cancellation checkpoints, including those inside the bound index
// comparator. No deadline, synthetic comparator, or production wiring override.
// Composite (1,NULL) forces the real first-key comparator before the second-key
// NULL rejection, so a cross-product cannot hide behind a NULL-only fast return.
type materializedWorkContext struct {
	context.Context
	operations int
}

func (c *materializedWorkContext) Err() error {
	c.operations++
	return c.Context.Err()
}

func TestMaterializedRefreshConcurrentWorkBound(t *testing.T) {
	const count = 1024
	t.Run("valid_composite_null_group", func(t *testing.T) {
		e := New()
		d := concurrentUnitRelation(t, e)
		index := materializedConcurrentUnitIndex(t, e, d, "unit_u", "i,p", "")
		d = e.catalog.state.MustRelation(d.Address())
		rows := concurrentUnitRows(t, d, make([]any, count)...)
		d = concurrentUnitReplace(t, e, d, rows, false, true)
		old := e.storage.Relations[d.Address()]
		ordering, err := bindIndexOrdering(d, index.SQLIndex)
		if err != nil {
			t.Fatal(err)
		}
		entries, err := materializedNewEntries(context.Background(), ordering, rows)
		if err != nil {
			t.Fatal(err)
		}
		ctx := &materializedWorkContext{Context: context.Background()}
		fresh := concurrentUnitRows(t, d, make([]any, count)...)
		effective, err := materializedConcurrentRows(ctx, e.CatalogRead(), d, old, e.visibility.CommitSeq(), entries, []*catalog.Relation{index}, fresh, sqltype.OutputEnvironment{})
		if err != nil {
			t.Fatal(err)
		}
		if len(effective) != count || !reflect.DeepEqual(effective, fresh) {
			t.Fatal("NULL-distinct refresh lost producer multiplicity", len(effective))
		}
		for i := range effective {
			if &effective[i][0] != &fresh[i][0] || &effective[i][0] == &rows[i][0] {
				t.Fatal("NULL key survived instead of transferring unmatched producer", i)
			}
		}
		// Sorting/group traversal is permitted; N-squared matching is not.
		if ctx.operations > 128*count {
			t.Fatalf("valid NULL group used %d operations; bound %d", ctx.operations, 128*count)
		}
	})
	t.Run("earliest_duplicate_prunes_remaining_pairs", func(t *testing.T) {
		e := New()
		d := concurrentUnitRelation(t, e)
		index := materializedConcurrentUnitIndex(t, e, d, "unit_u", "i", "")
		d = e.catalog.state.MustRelation(d.Address())
		payloads := make([]any, count)
		for i := range payloads {
			payloads[i] = int64(10)
		}
		rows := concurrentUnitRows(t, d, payloads...)
		ordering, err := bindIndexOrdering(d, index.SQLIndex)
		if err != nil {
			t.Fatal(err)
		}
		entries, err := materializedNewEntries(context.Background(), ordering, rows)
		if err != nil {
			t.Fatal(err)
		}
		ctx := &materializedWorkContext{Context: context.Background()}
		err = materializedDuplicatePreflight(ctx, e.CatalogRead(), d, ordering, entries, rows, sqltype.OutputEnvironment{})
		diagnostic := materializedConcurrentUnitError(t, err, "21000", `new data for materialized view "unit_mv" contains duplicate rows without any null columns`)
		if diagnostic.Detail != "Row: (1,10)" {
			t.Fatal("earliest producer diagnostic", diagnostic)
		}
		if ctx.operations > 16*count {
			t.Fatalf("earliest duplicate used %d operations; bound %d", ctx.operations, 16*count)
		}
	})
}

func TestMaterializedRefreshConcurrentDuplicateDiagnosticOrder(t *testing.T) {
	e := New()
	d := concurrentUnitRelation(t, e)
	index := materializedConcurrentUnitIndex(t, e, d, "unit_u", "i", "")
	d = e.catalog.state.MustRelation(d.Address())
	rows := concurrentUnitRows(t, d, int64(10), int64(10), int64(20), int64(20), int64(10))
	for i, key := range []int64{9, 8, 1, 1, 8} {
		value, err := sqltype.Integer(d.Columns[0].Usage.Type, key)
		if err != nil {
			t.Fatal(err)
		}
		rows[i][0] = value
	}
	ordering, err := bindIndexOrdering(d, index.SQLIndex)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := materializedNewEntries(context.Background(), ordering, rows)
	if err != nil {
		t.Fatal(err)
	}
	// Key order finds duplicate producer position two first. Position one in
	// a later key group must improve it; unique position zero cannot end search.
	err = materializedDuplicatePreflight(context.Background(), e.CatalogRead(), d, ordering, entries, rows, sqltype.OutputEnvironment{})
	diagnostic := materializedConcurrentUnitError(t, err, "21000", `new data for materialized view "unit_mv" contains duplicate rows without any null columns`)
	if diagnostic.Detail != "Row: (8,10)" {
		t.Fatal("key order replaced earliest producer diagnostic", diagnostic)
	}
}
