package session

import (
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
)

func TestPrivateStorage(t *testing.T) {
	a := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20100}
	b := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20101}
	published := &storage.Relation{Generation: 1}
	replacement := &storage.Relation{Generation: 2}
	store := &storage.Store{Relations: map[ids.ObjectAddress]*storage.Relation{a: published, b: published}}
	journal := &Journal{Storage: map[ids.ObjectAddress]StorageChange{a: {Replacement: replacement}}}
	if got := PrivateStorage(store, nil, a); got != published {
		t.Fatalf("nil journal storage = %p, want published %p", got, published)
	}
	if got := PrivateStorage(store, journal, a); got != replacement {
		t.Fatalf("changed storage = %p, want replacement %p", got, replacement)
	}
	if got := PrivateStorage(store, journal, b); got != published {
		t.Fatalf("unchanged storage = %p, want published %p", got, published)
	}
	dropped := &Journal{Storage: map[ids.ObjectAddress]StorageChange{a: {}}}
	if got := PrivateStorage(store, dropped, a); got != nil {
		t.Fatalf("dropped storage = %p, want nil", got)
	}
}

func TestCommittedRoots(t *testing.T) {
	namespace := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 2200}
	table := journalTestRelation(20110, "t", namespace)
	orphan := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20111}
	stored := &storage.Relation{Versions: make([]storage.RowVersion, 2), Ends: &storage.DeletionRoot{}}
	store := &storage.Store{Relations: map[ids.ObjectAddress]*storage.Relation{table.Address(): stored, orphan: {}}}
	roots := CommittedRoots(journalTestState(table), store)
	if len(roots) != 1 {
		t.Fatalf("roots = %v, want only the described relation", roots)
	}
	root := roots[table.Address()]
	if root.Storage != stored || root.Relation != table || len(root.Versions) != 2 || root.Ends != stored.Ends {
		t.Fatalf("root = %+v, want the published storage and descriptor", root)
	}
}

func TestNeedsSnapshotProjection(t *testing.T) {
	a := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20120}
	old := &catalog.Relation{ObjectAddress: a, Attributes: []int32{1}}
	next := &catalog.Relation{ObjectAddress: a, Attributes: []int32{1, 2}}
	roots := map[ids.ObjectAddress]visibility.Root{a: {Relation: old}}
	changed := &Journal{Storage: map[ids.ObjectAddress]StorageChange{a: {Relation: next}}}
	for _, tc := range []struct {
		name      string
		isolation Isolation
		journal   *Journal
		roots     map[ids.ObjectAddress]visibility.Root
		want      bool
	}{
		{"stable_shape_change", RepeatableRead, changed, roots, true},
		{"read_committed", ReadCommitted, changed, roots, false},
		{"no_journal", RepeatableRead, nil, roots, false},
		{"no_root", RepeatableRead, changed, nil, false},
		{"projected", Serializable, &Journal{Storage: map[ids.ObjectAddress]StorageChange{a: {Relation: next, HasSnapshot: true}}}, roots, false},
		{"same_shape", RepeatableRead, &Journal{Storage: map[ids.ObjectAddress]StorageChange{a: {Relation: old}}}, roots, false},
	} {
		owner := &TransactionOwner{Isolation: tc.isolation, Catalog: tc.journal}
		if got := owner.NeedsSnapshotProjection(tc.roots); got != tc.want {
			t.Errorf("%s: NeedsSnapshotProjection = %v, want %v", tc.name, got, tc.want)
		}
	}
}
