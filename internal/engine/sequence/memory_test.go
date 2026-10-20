package sequence

import (
	"maps"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

// The inspection accessors report the memory the mutating helpers built,
// without exposing the containers.
func TestSequenceSessionMemoryInspection(t *testing.T) {
	var s SessionMemory
	first := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 90001}
	second := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 90002}
	if s.Len() != 0 || len(maps.Collect(s.All())) != 0 {
		t.Fatal("empty session memory reported memories")
	}
	if _, ok := s.CachedPathKey(); ok {
		t.Fatal("empty path cache reported a key")
	}
	options := catalog.SequenceOptions{SQLType: sqltype.Int8, Increment: 1, Min: 1, Max: 100, Start: 1, Cache: 5}
	generator := &storage.SequenceGenerator{Last: 1, Generation: 3}
	m := s.DemandLocked(first)
	m.ObserveLocked(generator.Generation)
	if v, err := m.NextLocked(options, generator, "s"); err != nil || v != 1 {
		t.Fatal(v, err)
	}
	if m.CachedTail() != 4 || m.Generation() != 3 {
		t.Fatal("cache tail or generation", m.CachedTail(), m.Generation())
	}
	s.DemandLocked(second).SetLocked(7, true)
	got := maps.Collect(s.All())
	if s.Len() != 2 || len(got) != 2 || got[first] != m || got[second] != s.Memory(second) {
		t.Fatal("All/Len disagree with memory", s.Len(), got)
	}
	key := PathKey{Text: "public", Namespace: 9}
	s.StorePathLocked(key, Environment{ImplicitCatalog: true})
	if cached, ok := s.CachedPathKey(); !ok || cached != key {
		t.Fatal("cached path key", cached, ok)
	}
	m.DiscardTailLocked()
	if m.CachedTail() != 0 {
		t.Fatal("discarded tail still cached")
	}
}
