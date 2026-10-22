package typedef

import (
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
)

// Journals copied by value share their feature slots; writes go through a
// private copy so the other journal keeps its contents.
func TestCatalogTypeJournalSlotsCopyOnWrite(t *testing.T) {
	address := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 45000}
	root := new(storage.IndexRoot)
	fence := new(DomainValidationFence)
	original := session.NewJournal()
	DomainWritable(original).Validations = []*DomainValidationFence{fence}
	DomainWritable(original).IndexRoots = map[ids.ObjectAddress]*storage.IndexRoot{address: root}
	EnumWritable(original).Created = map[ids.ObjectAddress]bool{address: true}
	copied := *original
	DomainWritable(&copied).Validations = nil
	EnumWritable(&copied).Unsafe = map[int64]bool{9: true}
	if len(DomainJournalOf(original.Domain).ValidationFences()) != 1 || EnumJournalOf(original.Enum).UnsafeMembers() != nil || !EnumJournalOf(original.Enum).CreatedTypes()[address] {
		t.Fatal("write through a by-value journal copy changed the original")
	}

	clone := original.Clone()
	DomainJournalOf(clone.Domain).CommittedIndexRoots()[address] = nil
	delete(EnumJournalOf(clone.Enum).CreatedTypes(), address)
	if DomainJournalOf(original.Domain).CommittedIndexRoots()[address] != root || !EnumJournalOf(original.Enum).CreatedTypes()[address] {
		t.Fatal("clone shares slot maps with the original")
	}
	if empty := session.NewJournal().Clone(); empty.Domain != nil || empty.Enum != nil {
		t.Fatal("clone allocated slots for an empty journal")
	}
}
