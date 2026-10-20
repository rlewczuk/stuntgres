package engine

import (
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
)

// Journals copied by value share their feature slots; writes go through a
// private copy so the other journal keeps its contents.
func TestCatalogTypeJournalSlotsCopyOnWrite(t *testing.T) {
	address := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 45000}
	root := new(storage.IndexRoot)
	fence := new(domainValidationFence)
	original := newCatalogChanges()
	original.domainWritable().validations = []*domainValidationFence{fence}
	original.domainWritable().indexRoots = map[ids.ObjectAddress]*storage.IndexRoot{address: root}
	original.enumWritable().created = map[ids.ObjectAddress]bool{address: true}
	copied := *original
	copied.domainWritable().validations = nil
	copied.enumWritable().unsafe = map[int64]bool{9: true}
	if len(original.domain.validationFences()) != 1 || original.enum.unsafeMembers() != nil || !original.enum.createdTypes()[address] {
		t.Fatal("write through a by-value journal copy changed the original")
	}

	clone := cloneCatalogChanges(original)
	clone.domain.committedIndexRoots()[address] = nil
	delete(clone.enum.createdTypes(), address)
	if original.domain.committedIndexRoots()[address] != root || !original.enum.createdTypes()[address] {
		t.Fatal("clone shares slot maps with the original")
	}
	if empty := cloneCatalogChanges(newCatalogChanges()); empty.domain != nil || empty.enum != nil {
		t.Fatal("clone allocated slots for an empty journal")
	}
}
