package tablecmds_test

import (
	"context"
	"math"
	"reflect"
	"slices"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestVacuumFullStorage(t *testing.T) {
	e := engine.New()
	text, _ := e.CatalogRead().LookupType(sqltype.Text)
	payload := []sqltype.Value{sqltype.String(text, "retained")}
	rows := []storage.RowVersion{{Row: 1, Version: 11, Born: 1, InsertFullXID: 3, Values: payload}, {Row: 2, Version: 22, Born: 1, InsertFullXID: 4, Values: payload}, {Row: 3, Version: 33, Born: 2, InsertFullXID: 5, Values: payload}}
	ends := &storage.DeletionRoot{Rows: map[ids.RowID]storage.RowEnd{1: {Version: 11, End: 2}}}
	original := slices.Clone(rows)
	for _, retire := range []bool{false, true} {
		c, err := storage.RewriteVacuum(context.Background(), rows, ends, nil, 2, 5, retire, nil)
		if err != nil {
			t.Fatal(err)
		}
		want := 3
		if retire {
			want = 2
		}
		if len(c.Versions) != want || &c.Versions[0] == &rows[0] || c.Ends == ends || c.Positions == nil {
			t.Fatal("rewrite did not own fresh roots", c)
		}
		for i, row := range rows {
			position := c.Positions[i]
			if retire && i == 0 {
				if position != -1 {
					t.Fatal("removed position retained")
				}
				continue
			}
			copy := c.Versions[position]
			if copy.Row != row.Row || copy.Version != row.Version || copy.Born != row.Born || copy.InsertFullXID != row.InsertFullXID || &copy.Values[0] != &payload[0] {
				t.Fatal("identity/payload changed", copy, row)
			}
			if copy.Frozen != (row.InsertFullXID < 5) {
				t.Fatal("unjustified freeze", copy)
			}
		}
		if !reflect.DeepEqual(rows, original) || len(ends.Rows) != 1 {
			t.Fatal("retained predecessor mutated")
		}
		maintenance := storage.Maintenance{Live: 9, Dead: 8, Inserts: 3, Deletes: 1, Modifications: 4, InsertsSince: 3, VacuumCount: 7}
		next, err := storage.NewRewriteRelation(c, 8, maintenance)
		if err != nil || next.Generation != 9 || next.IndexSource != nil || next.Maintenance.Live != 9 || next.Maintenance.Dead != 8 || next.Maintenance.InsertsSince != 3 || next.Maintenance.VacuumCount != 7 || next.Maintenance.Modifications != 4 {
			t.Fatal("rewrite maintenance/incarnation", next, err)
		}
	}
	for _, input := range [][]storage.RowVersion{nil, {}, rows[1:]} {
		c, err := storage.RewriteVacuum(context.Background(), input, nil, nil, 2, 3, true, nil)
		if err != nil || c.Ends == nil || c.Versions == nil {
			t.Fatal("empty/clean rewrite must own roots", c, err)
		}
		a, _ := storage.NewRewriteRelation(c, 1, storage.Maintenance{})
		b, _ := storage.NewRewriteRelation(c, 1, storage.Maintenance{})
		if a == b {
			t.Fatal("new incarnation reused")
		}
	}
	allEnds := &storage.DeletionRoot{Rows: map[ids.RowID]storage.RowEnd{1: {Version: 11, End: 3}, 2: {Version: 22, End: 3}, 3: {Version: 33, End: 3}}}
	all, err := storage.RewriteVacuum(context.Background(), rows, allEnds, nil, 3, 9, true, nil)
	if err != nil || all.Removed != 3 || len(all.Versions) != 0 || len(all.Ends.Rows) != 0 || len(allEnds.Rows) != 3 {
		t.Fatal("all-dead rewrite", all, err)
	}
	frozen := slices.Clone(rows)
	for i := range frozen {
		frozen[i].Frozen = true
	}
	clean, err := storage.RewriteVacuum(context.Background(), frozen, nil, nil, 3, 9, true, nil)
	if err != nil || clean.Frozen != 0 || &clean.Versions[0] == &frozen[0] {
		t.Fatal("already-frozen heap reused header backing", clean, err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := storage.RewriteVacuum(canceled, rows, ends, nil, 2, 9, true, nil); err != context.Canceled {
		t.Fatal(err)
	}
	if _, err := storage.NewRewriteRelation(storage.VacuumCandidate{}, ^uint64(0), storage.Maintenance{}); err == nil {
		t.Fatal("generation overflow accepted")
	}
	if _, err := storage.NewRewriteRelation(storage.VacuumCandidate{Live: math.MaxInt64, Dead: 1}, 1, storage.Maintenance{}); err == nil {
		t.Fatal("rewrite tuple estimate overflow accepted")
	}
	history := &storage.HeapPredecessor{Version: rows[0], End: 2, EndFullXID: 7, Next: &storage.HeapPredecessor{Version: rows[1], End: 2, EndFullXID: 7}}
	for _, retire := range []bool{false, true} {
		candidate, err := storage.RewriteVacuum(context.Background(), rows[2:], nil, history, 2, 3, retire, nil)
		dead, removed, root := int64(2), int64(0), history
		if retire {
			dead, removed, root = 0, 2, nil
		}
		if err != nil || candidate.Live != 1 || candidate.Dead != dead || candidate.Removed != removed || candidate.Retained != root || len(candidate.Versions) != 1 || candidate.Positions[0] != 0 {
			t.Fatal("retained count contaminated current-copy sizing/mapping", candidate, err)
		}
		next, err := storage.NewRewriteRelation(candidate, 1, storage.Maintenance{})
		if err != nil || next.Retained != root || next.Maintenance.Tuples != 1+dead {
			t.Fatal("fresh rewrite omitted exact retained root/class estimate", next, err)
		}
	}
	for _, invalid := range []*storage.HeapPredecessor{{Version: rows[0]}, {Version: rows[0], End: 3}, {End: 2}} {
		if _, err := storage.RewriteVacuum(context.Background(), rows, ends, invalid, 2, 3, true, nil); err == nil {
			t.Fatal("invalid committed physical history accepted")
		}
	}
	retainedContext, retainedCancel := context.WithCancel(context.Background())
	defer retainedCancel()
	if _, err := storage.RewriteVacuum(retainedContext, rows, ends, history, 2, 3, true, func(int, int64) { retainedCancel() }); err != context.Canceled {
		t.Fatal("retained inspection ignored cancellation after current copy", err)
	}
	if history.End != 2 || history.EndFullXID != 7 || history.Next.End != 2 || !reflect.DeepEqual(history.Version, rows[0]) || !reflect.DeepEqual(history.Next.Version, rows[1]) {
		t.Fatal("rewrite/failure mutated borrowed physical history")
	}
}
