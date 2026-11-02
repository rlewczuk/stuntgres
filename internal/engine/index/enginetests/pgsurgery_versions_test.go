package enginetests_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqltype"
)

func TestPgsurgeryFreezeIndexVersionsProjection(t *testing.T) {
	parent := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 90000}
	idx := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 90001}
	a := storage.RowVersion{Row: 7, Version: 11}
	b := storage.RowVersion{Row: 7, Version: 12, Base: 10}
	var registry index.ExclusionRegistry
	ticket := new(lock.Ticket)
	initial := registry.ProjectionLocked(ticket)
	edit := index.ExclusionEdit{Index: idx, Member: true, Admitted: true}
	edit.Apply(initial, parent, a)
	edit.Apply(initial, parent, b)
	if len(initial.Entries[idx]) != 2 {
		t.Fatal("second actual projection replaced sibling")
	}
	registry.InstallLocked(ticket, initial)
	captured := registry.OwnLocked(ticket)
	updated := registry.ProjectionLocked(ticket)
	edit.Apply(updated, parent, storage.RowVersion{Row: 7, Version: 13, Base: 11})
	if len(updated.Entries[idx]) != 2 || updated.Entries[idx][0].Row.Version != 12 || updated.Entries[idx][1].Row.Version != 13 {
		t.Fatal("exact base replacement removed unrelated sibling")
	}
	next := registry.ProjectionLocked(ticket)
	next.EndRow(parent, a.Row, a.Version)
	if len(next.Entries[idx]) != 1 || next.Entries[idx][0].Row.Version != b.Version || next.Ended[parent][a.Version] != a.Row {
		t.Fatal("exact end removed sibling", next)
	}
	if len(captured.Entries[idx]) != 2 || len(captured.Ended[parent]) != 0 {
		t.Fatal("published projection changed")
	}
	registry.InstallLocked(ticket, next)
	both := registry.ProjectionLocked(ticket)
	excluded := index.ExclusionEdit{Index: idx, Excluded: []storage.RowVersion{a, b}}
	excluded.Apply(both, parent, storage.RowVersion{Row: 8, Version: 13})
	if len(both.Ended[parent]) != 3 || both.Ended[parent][11] != 7 || both.Ended[parent][12] != 7 || both.Ended[parent][10] != 7 {
		t.Fatal("excluded siblings/base collapsed", both.Ended)
	}
	if len(next.Ended[parent]) != 1 {
		t.Fatal("later edit mutated captured end map")
	}
	deletes := &visibility.PrivateDeletes{}
	deletes.Set(visibility.DeleteCandidate{Row: 7, Version: 11})
	deletes.Set(visibility.DeleteCandidate{Row: 7, Version: 12})
	shape := registry.ProjectionLocked(ticket)
	descriptor := &catalog.Relation{ObjectAddress: parent}
	shape.ReplaceShape(descriptor, descriptor, nil, nil, deletes)
	if shape.Ended[parent][11] != 7 || shape.Ended[parent][12] != 7 {
		t.Fatal("shape transport collapsed actual ends")
	}
	if len(next.Ended[parent]) != 1 {
		t.Fatal("shape transport mutated predecessor")
	}
}

func TestPgsurgeryFreezeIndexVersionsDelta(t *testing.T) {
	for _, method := range []catalog.IndexMethod{catalog.BtreeMethod, catalog.HashMethod} {
		t.Run(method.String(), func(t *testing.T) {
			view := engine.New().CatalogRead()
			integer, _ := view.LookupType(sqltype.Int4)
			boolean, _ := view.LookupType(sqltype.Bool)
			value := func(n int64) sqltype.Value {
				v, err := sqltype.Integer(integer, n)
				if err != nil {
					t.Fatal(err)
				}
				return v
			}
			parent := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 90000}, Attributes: []int32{1}, Columns: []catalog.Column{{Name: "k", Usage: integer.Usage()}}}
			d := &catalog.IndexDefinition{Method: method, Parent: parent.Address(), Keys: []catalog.IndexKey{{Attribute: 1}}, Ready: true, Valid: true, Live: true}
			if method == catalog.BtreeMethod {
				d.MethodOID = 403
			} else {
				d.MethodOID = 405
			}
			truth := plan.LiteralExpr{Info: expr.Info{Usage: boolean.Usage()}, Value: sqltype.Boolean(boolean, true)}
			if method == catalog.BtreeMethod {
				d.Predicate = &catalog.IndexPredicate{Definition: truth, Execution: truth}
			}
			idx := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 90001}, SQLIndex: d}
			a := storage.RowVersion{Row: 7, Version: 11, Born: 1, Values: []sqltype.Value{value(1)}}
			b := storage.RowVersion{Row: 7, Version: 12, Born: 2, Values: []sqltype.Value{value(2)}}
			store := &storage.Relation{Versions: []storage.RowVersion{b}}
			eval := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Catalog: view}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
			old, err := index.BuildIndexRoot(eval, parent, d, store, nil)
			if err != nil {
				t.Fatal(err)
			}
			store.Retained = storage.RetainHeapPredecessor(a)
			store.Retained.End = 2
			store.Headers, err = storage.RepairHeapHeaders(nil, []storage.RowVersion{a})
			if err != nil {
				t.Fatal(err)
			}
			// Surgery itself does not manufacture the missing predecessor index entry.
			if method == catalog.BtreeMethod && (len(old.Entries) != 1 || old.Entries[0].Version != 12) {
				t.Fatal("repair recreated index entries")
			}
			writes := []storage.RowVersion{{Row: 7, Version: 13, Base: 11, Values: []sqltype.Value{value(3)}}, {Row: 7, Version: 14, Base: 12, Values: []sqltype.Value{value(4)}}}
			publication := &index.RowPublication{Store: store, Root: store.Versions, Writes: writes, Next: writes, Slots: []int{0, 1}}
			var merged *storage.IndexRoot
			if method == catalog.HashMethod {
				merged, err = index.MergeHashIndexRoot(eval, parent, idx, old, publication, true, true)
			} else {
				merged, err = index.MergeIndexRoot(eval, parent, idx, old.Entries, publication, true, true, false, "", sqltype.OutputEnvironment{}, nil, nil)
			}
			if err != nil {
				t.Fatal(err)
			}
			if method == catalog.BtreeMethod {
				if len(merged.Entries) != 2 || !merged.IsMember(7, 13) || !merged.IsMember(7, 14) {
					t.Fatal("ordinary sibling writes lost membership", merged)
				}
				merged.RowEntries = make(map[ids.RowID]int)
				for at := range merged.Entries {
					merged.SetRowEntry(at)
				}
				bound := &index.UniqueBoundKey{Index: idx}
				for _, row := range writes {
					member, keys, err := index.ExistingRootOutput(bound, merged, row, eval, index.ProbeRevalidation)
					if err != nil || !member || len(keys) != 1 || !sqltype.SameDatum(keys[0], row.Values[0]) {
						t.Fatal("probe shadowed exact sibling", row.Version, member, keys, err)
					}
				}
				if member, _, err := index.ExistingRootOutput(bound, merged, a, eval, index.ProbeRevalidation); err != nil || member {
					t.Fatal("absent predecessor gained membership", member, err)
				}
			}
			if method == catalog.HashMethod {
				merged.Hash.RowEntries = make(map[ids.RowID]*storage.HashEntry)
				for _, bucket := range merged.Hash.Buckets {
					for at := range bucket {
						merged.Hash.SetRowEntry(&bucket[at])
					}
				}
				bound := &index.UniqueBoundKey{Index: idx, Scratch: make([]sqltype.Value, 1)}
				for _, row := range writes {
					member, keys, err := index.ExistingRootOutput(bound, merged, row, eval, index.ProbeRevalidation)
					if err != nil || !member || !sqltype.SameDatum(keys[0], row.Values[0]) {
						t.Fatal("hash probe shadowed exact sibling", row.Version, member, keys, err)
					}
				}
				if member, _, err := index.ExistingRootOutput(bound, merged, a, eval, index.ProbeRevalidation); err != nil || member {
					t.Fatal("absent hash predecessor gained membership", member, err)
				}
				var count int
				for _, bucket := range old.Hash.Buckets {
					for _, entry := range bucket {
						count++
						if entry.Version != 12 {
							t.Fatal("repair recreated hash entry")
						}
					}
				}
				if count != 1 {
					t.Fatal("immutable hash predecessor changed")
				}
			}
			for _, targets := range [][]ids.VersionID{{13}, {14}, {13, 14}} {
				deletes := &visibility.PrivateDeletes{}
				for _, version := range targets {
					deletes.Set(visibility.DeleteCandidate{Row: 7, Version: version})
				}
				p := &index.RowPublication{Store: store, Root: writes, Next: writes, Deletion: deletes}
				var retired *storage.IndexRoot
				if method == catalog.HashMethod {
					retired, err = index.MergeHashIndexRoot(eval, parent, idx, merged, p, false, true)
				} else {
					retired, err = index.MergeIndexRoot(eval, parent, idx, merged.Entries, p, false, true, false, "", sqltype.OutputEnvironment{}, nil, nil)
				}
				if err != nil {
					t.Fatal(err)
				}
				var versions []ids.VersionID
				if method == catalog.HashMethod {
					for _, bucket := range retired.Hash.Buckets {
						for _, entry := range bucket {
							versions = append(versions, entry.Version)
						}
					}
				} else {
					for _, entry := range retired.Entries {
						versions = append(versions, entry.Version)
					}
				}
				if len(versions) != 2-len(targets) {
					t.Fatal("sibling end count collapsed", targets, versions)
				}
				for _, version := range versions {
					for _, target := range targets {
						if version == target {
							t.Fatal("target index member survived", target)
						}
					}
				}
			}
			if method == catalog.BtreeMethod && (len(old.Entries) != 1 || old.Entries[0].Version != 12 || len(merged.Entries) != 2 || !merged.IsMember(7, 13) || !merged.IsMember(7, 14)) {
				t.Fatal("delta mutated immutable predecessor")
			}
		})
	}
}

func TestPgsurgeryFreezeIndexVersionsGIN(t *testing.T) {
	f := newGinFixture(t)
	entries := []storage.IndexEntry{{Row: 7, Version: 11, Position: 0, Keys: f.root.Entries[0].Keys}, {Row: 7, Version: 12, Position: 1, Keys: f.root.Entries[1].Keys}, {Row: 8, Version: 13, Position: 2, Keys: f.root.Entries[0].Keys}}
	bindings := make([]sqltype.GinBinding, len(f.root.Gin.Columns))
	for at, column := range f.root.Gin.Columns {
		bindings[at] = column.Binding
	}
	old, err := storage.BuildGin(context.Background(), bindings, entries, sqltype.InputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	for _, targets := range []map[ids.VersionID]ids.RowID{{11: 7}, {12: 7}, {11: 7, 12: 7}} {
		next, err := storage.MergeGin(context.Background(), old, nil, targets, sqltype.InputEnvironment{})
		if err != nil {
			t.Fatal(err)
		}
		for _, column := range next.Columns {
			if len(column.NonNull) != 3-len(targets) {
				t.Fatal("GIN sibling posting count collapsed", column.NonNull)
			}
			for _, posting := range column.NonNull {
				if targets[posting.Version] == posting.Row {
					t.Fatal("target posting survived", posting)
				}
			}
			for _, list := range column.Tokens {
				for _, posting := range list.Postings {
					if targets[posting.Version] == posting.Row {
						t.Fatal("target token posting survived", posting)
					}
				}
			}
		}
	}
	for _, column := range old.Columns {
		if len(column.NonNull) != 3 {
			t.Fatal("GIN merge mutated immutable postings")
		}
	}
}
