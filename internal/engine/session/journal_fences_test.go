package session

import (
	"errors"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
)

func journalTestRelation(id ids.OID, name string, namespace ids.ObjectAddress) *catalog.Relation {
	return &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: id}, ObjectNamespace: namespace, ObjectName: name}
}

func journalTestIndex(id ids.OID, name string, parent *catalog.Relation) *catalog.Relation {
	r := journalTestRelation(id, name, parent.Namespace())
	r.SQLIndex = &catalog.IndexDefinition{Parent: parent.Address()}
	return r
}

func journalTestState(descriptors ...catalog.Descriptor) *catalog.State {
	s := catalog.NewState(0)
	for _, d := range descriptors {
		s.SetDescriptor(d.Address(), d)
		s.BindName(catalog.DescriptorNameKey(d), d.Address())
	}
	return s
}

func isConflict(err error) bool {
	var conflict catalog.Conflict
	return errors.As(err, &conflict)
}

func TestJournalStorageReadFence(t *testing.T) {
	a := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20010}
	store := &storage.Relation{Versions: make([]storage.RowVersion, 1), Generation: 3}
	fence := NewStorageReadFence(a, store)
	if fence.Kind() != FenceStorageRead || fence.Address() != a || !fence.Matches(store) || fence.Matches(nil) {
		t.Fatal("a storage read did not record its storage")
	}
	if err := fence.Current(nil, map[ids.ObjectAddress]*storage.Relation{a: store}); err != nil {
		t.Fatal(err)
	}
	same := *store
	if err := fence.Current(nil, map[ids.ObjectAddress]*storage.Relation{a: &same}); !isConflict(err) {
		t.Fatalf("replaced storage: got %v, want a conflict", err)
	}
	store.Versions = append(store.Versions, storage.RowVersion{})
	if err := fence.Current(nil, map[ids.ObjectAddress]*storage.Relation{a: store}); !isConflict(err) {
		t.Fatalf("grown storage: got %v, want a conflict", err)
	}
	absent := NewStorageReadFence(a, nil)
	if err := absent.Current(nil, nil); err != nil || !absent.Matches(nil) {
		t.Fatal("an absent read did not hold while the storage stayed absent", err)
	}
	if err := absent.Current(nil, map[ids.ObjectAddress]*storage.Relation{a: store}); !isConflict(err) {
		t.Fatalf("created storage: got %v, want a conflict", err)
	}
}

func TestJournalIndexSetAndNamespaceFences(t *testing.T) {
	schema := journalTestSchema(20001, "s")
	table := journalTestRelation(20010, "t", schema.Address())
	index := journalTestIndex(20011, "t_i", table)
	committed := journalTestState(schema, table, index)
	set := NewIndexSetFence(committed, table.Address())
	members := NewNamespaceFence(committed, schema.Address())
	if set.Kind() != FenceIndexSet || members.Kind() != FenceNamespace {
		t.Fatal("wrong fence kinds")
	}
	if err := set.Current(committed, nil); err != nil {
		t.Fatal(err)
	}
	if err := members.Current(committed, nil); err != nil {
		t.Fatal(err)
	}

	added := catalog.CloneState(committed)
	other := journalTestIndex(20012, "t_j", table)
	added.SetDescriptor(other.Address(), other)
	if !isConflict(set.Current(added, nil)) || !isConflict(members.Current(added, nil)) {
		t.Fatal("an added index did not break the index-set and namespace fences")
	}
	dropped := catalog.CloneState(committed)
	dropped.RemoveDescriptor(index.Address())
	if !isConflict(set.Current(dropped, nil)) || !isConflict(members.Current(dropped, nil)) {
		t.Fatal("a dropped index did not break the index-set and namespace fences")
	}
	replaced := catalog.CloneState(committed)
	next := *table
	replaced.SetDescriptor(table.Address(), &next)
	if set.Current(replaced, nil) != nil || !isConflict(members.Current(replaced, nil)) {
		t.Fatal("a replaced member must break only the namespace fence")
	}
}
