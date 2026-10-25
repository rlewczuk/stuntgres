package session

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
)

// StorageReadFence records the committed storage a stage read, so publication
// fails when that storage changed. Partition CREATE and ATTACH scans, journal
// captures and overlay-index reads record it.
type StorageReadFence struct {
	address    ids.ObjectAddress
	storage    *storage.Relation
	length     int
	ends       *storage.DeletionRoot
	generation uint64
}

// NewStorageReadFence records store, the storage read at a; a nil store
// records its absence.
func NewStorageReadFence(a ids.ObjectAddress, store *storage.Relation) StorageReadFence {
	f := StorageReadFence{address: a, storage: store}
	if store != nil {
		f.length, f.ends, f.generation = len(store.Versions), store.Ends, store.Generation
	}
	return f
}

func (StorageReadFence) Kind() FenceKind { return FenceStorageRead }

// Address returns the address whose storage was read.
func (f StorageReadFence) Address() ids.ObjectAddress { return f.address }

// Matches reports whether s is the recorded storage, unchanged.
func (f StorageReadFence) Matches(s *storage.Relation) bool {
	return s == f.storage && (s == nil || len(s.Versions) == f.length && s.Ends == f.ends && s.Generation == f.generation)
}

func (f StorageReadFence) Current(_ *catalog.State, relations map[ids.ObjectAddress]*storage.Relation) error {
	if !f.Matches(relations[f.address]) {
		return catalog.Conflict{}
	}
	return nil
}

// IndexSetFence records the committed indexes of a parent relation, so
// publication fails when one of its indexes was created or dropped.
type IndexSetFence struct {
	parent  ids.ObjectAddress
	indexes map[ids.ObjectAddress]bool
}

// NewIndexSetFence records the indexes of parent in committed.
func NewIndexSetFence(committed *catalog.State, parent ids.ObjectAddress) IndexSetFence {
	f := IndexSetFence{parent: parent, indexes: map[ids.ObjectAddress]bool{}}
	for a, r := range committed.Relations() {
		if r.SQLIndex != nil && r.SQLIndex.Parent == parent {
			f.indexes[a] = true
		}
	}
	return f
}

func (IndexSetFence) Kind() FenceKind { return FenceIndexSet }

func (f IndexSetFence) Current(committed *catalog.State, _ map[ids.ObjectAddress]*storage.Relation) error {
	count := 0
	for a, r := range committed.Relations() {
		if r.SQLIndex != nil && r.SQLIndex.Parent == f.parent {
			if !f.indexes[a] {
				return catalog.Conflict{}
			}
			count++
		}
	}
	if count != len(f.indexes) {
		return catalog.Conflict{}
	}
	return nil
}

// NamespaceFence records the committed members of a removed namespace, so
// publication fails when a member changed or was added.
type NamespaceFence struct {
	namespace ids.ObjectAddress
	members   map[ids.ObjectAddress]catalog.Descriptor
}

// NewNamespaceFence records the members of namespace in committed.
func NewNamespaceFence(committed *catalog.State, namespace ids.ObjectAddress) NamespaceFence {
	f := NamespaceFence{namespace: namespace, members: map[ids.ObjectAddress]catalog.Descriptor{}}
	for a, d := range committed.Descriptors() {
		if d.Namespace() == namespace {
			f.members[a] = d
		}
	}
	return f
}

func (NamespaceFence) Kind() FenceKind { return FenceNamespace }

func (f NamespaceFence) Current(committed *catalog.State, _ map[ids.ObjectAddress]*storage.Relation) error {
	count := 0
	for a, d := range committed.Descriptors() {
		if d.Namespace() == f.namespace {
			if f.members[a] != d {
				return catalog.Conflict{}
			}
			count++
		}
	}
	if count != len(f.members) {
		return catalog.Conflict{}
	}
	return nil
}
