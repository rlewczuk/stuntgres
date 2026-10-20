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
	original.domainWritable().Validations = []*typedef.DomainValidationFence{fence}
	original.domainWritable().IndexRoots = map[ids.ObjectAddress]*storage.IndexRoot{address: root}
	original.enumWritable().Created = map[ids.ObjectAddress]bool{address: true}
	copied := *original
	copied.domainWritable().Validations = nil
	copied.enumWritable().Unsafe = map[int64]bool{9: true}
	if len(original.domain.ValidationFences()) != 1 || original.enum.UnsafeMembers() != nil || !original.enum.CreatedTypes()[address] {
		t.Fatal("write through a by-value journal copy changed the original")
	}

	clone := cloneCatalogChanges(original)
	clone.domain.CommittedIndexRoots()[address] = nil
	delete(clone.enum.CreatedTypes(), address)
	if original.domain.CommittedIndexRoots()[address] != root || !original.enum.CreatedTypes()[address] {
		t.Fatal("clone shares slot maps with the original")
	}
	if empty := cloneCatalogChanges(newCatalogChanges()); empty.domain != nil || empty.enum != nil {
		t.Fatal("clone allocated slots for an empty journal")
	}
}
