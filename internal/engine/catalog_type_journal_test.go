package engine

import (
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/typedef"
)

// Journals copied by value share their feature slots; writes go through a
// private copy so the other journal keeps its contents.
func TestCatalogTypeJournalSlotsCopyOnWrite(t *testing.T) {
	address := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 45000}
	root := new(storage.IndexRoot)
	fence := new(typedef.DomainValidationFence)
	original := newCatalogChanges()
	domainWritable(original).Validations = []*typedef.DomainValidationFence{fence}
	domainWritable(original).IndexRoots = map[ids.ObjectAddress]*storage.IndexRoot{address: root}
	enumWritable(original).Created = map[ids.ObjectAddress]bool{address: true}
	copied := *original
	domainWritable(&copied).Validations = nil
	enumWritable(&copied).Unsafe = map[int64]bool{9: true}
	if len(domainJournalOf(original.Domain).ValidationFences()) != 1 || enumJournalOf(original.Enum).UnsafeMembers() != nil || !enumJournalOf(original.Enum).CreatedTypes()[address] {
		t.Fatal("write through a by-value journal copy changed the original")
	}

	clone := cloneCatalogChanges(original)
	domainJournalOf(clone.Domain).CommittedIndexRoots()[address] = nil
	delete(enumJournalOf(clone.Enum).CreatedTypes(), address)
	if domainJournalOf(original.Domain).CommittedIndexRoots()[address] != root || !enumJournalOf(original.Enum).CreatedTypes()[address] {
		t.Fatal("clone shares slot maps with the original")
	}
	if empty := cloneCatalogChanges(newCatalogChanges()); empty.Domain != nil || empty.Enum != nil {
		t.Fatal("clone allocated slots for an empty journal")
	}
}
