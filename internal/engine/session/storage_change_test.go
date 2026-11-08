package session

import (
	"maps"
	"slices"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

func fillKeys(fills map[int32]sqltype.Value) []int32 {
	return slices.Sorted(maps.Keys(fills))
}

func TestStorageChangeMergeFillsReplacesSharedMap(t *testing.T) {
	shared := map[int32]sqltype.Value{1: {}, 2: {}}
	change := StorageChange{Fills: shared}
	change.MergeFills(map[int32]sqltype.Value{3: {}}, &catalog.Relation{Attributes: []int32{1, 3}})
	if got := fillKeys(change.Fills); !slices.Equal(got, []int32{1, 3}) {
		t.Fatalf("merged fills = %v, want [1 3]", got)
	}
	if got := fillKeys(shared); !slices.Equal(got, []int32{1, 2}) {
		t.Fatalf("shared fills mutated to %v", got)
	}
}

func TestStorageChangeMergeFillsWithoutFillsKeepsNil(t *testing.T) {
	var change StorageChange
	change.MergeFills(nil, &catalog.Relation{Attributes: []int32{1}})
	if change.Fills != nil {
		t.Fatalf("fills = %v, want nil", change.Fills)
	}
	change.MergeFills(map[int32]sqltype.Value{2: {}}, &catalog.Relation{Attributes: []int32{1}})
	if change.Fills == nil || len(change.Fills) != 0 {
		t.Fatalf("fills of dropped attribute = %v, want an empty map", change.Fills)
	}
}

func rewriteFillValue(t *testing.T, n int64) sqltype.Value {
	t.Helper()
	for _, typ := range sqltype.Builtins() {
		if typ.ID() == sqltype.Int8 {
			value, err := sqltype.Integer(typ, n)
			if err != nil {
				t.Fatal(err)
			}
			return value
		}
	}
	t.Fatal("missing bigint type")
	return sqltype.Value{}
}

func TestRecordRowShapesRewriteRetiresFills(t *testing.T) {
	for _, name := range []string{"populated", "empty", "private_create"} {
		t.Run(name, func(t *testing.T) {
			relation := journalTestRelation(20010, "t", ids.ObjectAddress{})
			relation.Attributes = []int32{1, 2, 3}
			candidate := catalog.NewCandidate(catalog.NewRead(&catalog.Owner{}, journalTestState(relation)))
			root := []storage.RowVersion{{Row: 1, Version: 10}}
			original := &storage.Relation{Versions: root}
			if name == "private_create" {
				original, root = nil, nil
			}
			replacement := &storage.Relation{}
			if name == "populated" {
				replacement.Versions = []storage.RowVersion{{Row: 1, Version: 20}}
			}
			ends := &storage.DeletionRoot{}
			journal := NewJournal()
			address := relation.Address()
			journal.Storage[address] = StorageChange{
				Original: original, Root: root, Ends: ends, Replacement: original,
				Fills:    map[int32]sqltype.Value{2: rewriteFillValue(t, -1), 3: rewriteFillValue(t, 7)},
				Snapshot: []storage.RowVersion{{Row: 1, Version: 10}}, HasSnapshot: true,
			}
			journal.RecordRowShapes(map[ids.ObjectAddress]storage.RowShape{
				address: {Replacement: replacement, Rewritten: true, Frontier: 42},
			}, &candidate)
			got := journal.Storage[address]
			if got.Fills != nil || got.Snapshot != nil || got.HasSnapshot {
				t.Fatalf("rewrite retained fills or snapshot: %#v", got)
			}
			if got.Replacement != replacement || !got.Rewritten || got.Frontier != 42 || got.Relation != relation {
				t.Fatalf("rewrite publication = %#v", got)
			}
			if got.Original != original || got.Ends != ends || len(got.Root) != len(root) {
				t.Fatalf("first predecessor changed: %#v", got)
			}
			if len(root) != 0 && &got.Root[0] != &root[0] {
				t.Fatal("first predecessor root replaced")
			}
		})
	}
}

func TestRecordRowShapesRewritePreservesEarlierJournal(t *testing.T) {
	relation := journalTestRelation(20010, "t", ids.ObjectAddress{})
	relation.Attributes = []int32{1, 2, 3}
	candidate := catalog.NewCandidate(catalog.NewRead(&catalog.Owner{}, journalTestState(relation)))
	address := relation.Address()
	fill := rewriteFillValue(t, -1)
	fresh := rewriteFillValue(t, 7)
	shared := map[int32]sqltype.Value{2: fill}
	root := []storage.RowVersion{{Row: 1, Version: 10}}
	snapshot := []storage.RowVersion{{Row: 1, Version: 11}}
	original := &storage.Relation{Versions: root}
	ends := &storage.DeletionRoot{}
	earlier := NewJournal()
	earlier.Storage[address] = StorageChange{
		Original: original, Root: root, Ends: ends, Replacement: original,
		Fills: shared, Snapshot: snapshot, HasSnapshot: true,
	}
	for _, replacement := range []*storage.Relation{original, {Versions: root}} {
		ordinary := earlier.Clone()
		ordinary.RecordRowShapes(map[ids.ObjectAddress]storage.RowShape{
			address: {Original: original, Replacement: replacement},
		}, &candidate)
		got := ordinary.Storage[address]
		if !got.HasSnapshot || &got.Snapshot[0] != &snapshot[0] || len(got.Fills) != 1 {
			t.Fatal("non-rewrite retired fills or stable snapshot")
		}
		if value, err := got.Fills[2].Integer(); err != nil || value != -1 {
			t.Fatal("non-rewrite changed fill", value, err)
		}
	}
	current := earlier.Clone()
	replacement := &storage.Relation{Versions: []storage.RowVersion{{Row: 1, Version: 20}}}
	current.RecordRowShapes(map[ids.ObjectAddress]storage.RowShape{
		address: {Replacement: replacement, Rewritten: true, Frontier: 42},
	}, &candidate)
	afterRewrite := current.Clone()
	later := &storage.Relation{Versions: replacement.Versions}
	current.RecordRowShapes(map[ids.ObjectAddress]storage.RowShape{
		address: {Original: replacement, Replacement: later},
	}, &candidate)
	added := current.Storage[address]
	added.MergeFills(map[int32]sqltype.Value{3: fresh}, relation)
	current.Storage[address] = added
	if !added.Rewritten || added.Frontier != 42 || len(added.Fills) != 1 {
		t.Fatalf("later ADD lost fresh fill or rewrite history: %#v", added)
	}
	if value, err := added.Fills[3].Integer(); err != nil || value != 7 {
		t.Fatal("later ADD changed fresh default", value, err)
	}
	if afterRewrite.Storage[address].Fills != nil {
		t.Fatal("later ADD mutated earlier rewrite journal")
	}
	beforeNext := current.Clone()
	next := &storage.Relation{Versions: []storage.RowVersion{{Row: 1, Version: 30}}}
	current.RecordRowShapes(map[ids.ObjectAddress]storage.RowShape{
		address: {Replacement: next, Rewritten: true, Frontier: 84},
	}, &candidate)
	got := current.Storage[address]
	if got.Fills != nil || got.Frontier != 84 || got.Replacement != next || got.Snapshot != nil || got.HasSnapshot {
		t.Fatalf("next rewrite did not retire current fills: %#v", got)
	}
	if got.Original != original || got.Ends != ends || &got.Root[0] != &root[0] {
		t.Fatal("rewrite sequence changed first predecessor")
	}
	if value, err := beforeNext.Storage[address].Fills[3].Integer(); err != nil || value != 7 {
		t.Fatal("next rewrite mutated predecessor fill", value, err)
	}
	old := earlier.Storage[address]
	if !old.HasSnapshot || &old.Snapshot[0] != &snapshot[0] || snapshot[0].Version != 11 ||
		old.Replacement != original || &old.Root[0] != &root[0] || root[0].Version != 10 {
		t.Fatal("rewrite mutated earlier journal or shared snapshot/root")
	}
	if value, err := shared[2].Integer(); err != nil || value != -1 || len(shared) != 1 || len(old.Fills) != 1 {
		t.Fatal("rewrite mutated shared fill map", value, err)
	}
}
