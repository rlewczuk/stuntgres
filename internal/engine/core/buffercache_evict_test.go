package core

import (
	"sync"
	"testing"

	"stuntgres/internal/engine/ids"
)

func TestPgBuffercacheEvictionCache(t *testing.T) {
	t.Run("registered_mutation_owner", func(t *testing.T) {
		e, err := NewEngine(nil, nil, "eviction_owner_boundary", Startup{})
		if err != nil { t.Fatal(err) }
		cluster := e.Cluster
		e.Mu.Lock()
		defer e.Mu.Unlock()
		defer func() { e.Cluster = cluster }()
		e.Cluster = nil
		if e.registeredLogicalBufferOwnerLocked() == nil || cluster.buffercache != nil {
			t.Fatal("private runtime acquired mutation cache authority")
		}
		e.Cluster = cluster
		database := e.Catalog.Current().MustDatabase(e.Catalog.DatabaseAddress())
		delete(cluster.Targets, database.Name())
		defer func() { cluster.Targets[database.Name()] = e }()
		if e.registeredLogicalBufferOwnerLocked() == nil || cluster.buffercache != nil {
			t.Fatal("unregistered runtime acquired mutation cache authority")
		}
		cluster.Targets[database.Name()] = e
		if err := e.registeredLogicalBufferOwnerLocked(); err != nil { t.Fatal(err) }
	})
	domain := new(ids.Domain)
	key := logicalBufferKey{domain: domain, epoch: 1, relation: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 17000}, node: 17000, space: 1663, database: 5}
	owner, peer := domain.Session(1), domain.Session(2)
	cache := &logicalBufferCache{capacity: 3}
	if cache.evictSlot(1) { t.Fatal("unused slot reported eviction") }
	if e, s := cache.evictMatching(nil); e != 0 || s != 0 || cache.bySlot != nil { t.Fatal("unused capacity counted/allocated", e, s) }
	first, _ := cache.open(key, owner)
	alias, _ := cache.open(key, owner)
	cache.open(key, peer)
	_, detached := cache.snapshot()
	if cache.evictSlot(first.slot) { t.Fatal("pinned entry removed") }
	if e, s := cache.evictMatching(&key); e != 0 || s != 1 { t.Fatal("one candidate counted as multiple backend/alias skips", e, s) }
	cache.close(first, owner)
	cache.close(first, peer)
	if cache.evictSlot(first.slot) { t.Fatal("remaining real alias lost pin") }
	cache.close(alias, owner)
	if !cache.evictSlot(first.slot) || cache.byKey[key] != nil || cache.bySlot[first.slot] != nil || first.owners != nil { t.Fatal("successful removal retained indexes/tokens") }
	if cache.evictSlot(first.slot) { t.Fatal("repeated unused removal succeeded") }
	replacement, _ := cache.open(key, peer)
	if replacement == first || replacement.slot != first.slot || replacement.usage != 1 { t.Fatal("fresh incarnation did not reuse cold slot", replacement) }
	cache.close(first, peer)
	if len(replacement.owners) != 1 || replacement.owners[peer] != 1 { t.Fatal("late stale close released replacement") }
	if detached[first.slot].usage != 3 || detached[first.slot].pins != 2 { t.Fatal("detached observation changed", detached) }
	otherKey := key
	otherKey.domain = new(ids.Domain) // Deliberate same numeric storage identity.
	other, _ := cache.open(otherKey, owner)
	cache.close(other, owner)
	if e, s := cache.evictMatching(&key); e != 0 || s != 1 || cache.byKey[otherKey] != other { t.Fatal("selector conflated catalog domains", e, s) }
	cache.close(replacement, peer)
	if e, s := cache.evictMatching(&key); e != 1 || s != 0 || cache.byKey[otherKey] != other { t.Fatal("exact target removal affected peer", e, s) }
	if e, s := cache.evictMatching(nil); e != 1 || s != 0 || len(cache.byKey) != 0 || len(cache.bySlot) != 0 { t.Fatal("all performed counts/index cleanup", e, s) }
	if e, s := cache.evictMatching(nil); e != 0 || s != 0 { t.Fatal("unused slots counted as skipped", e, s) }

	var workers sync.WaitGroup
	for range 4 {
		workers.Go(func() {
			for range 16 {
				entry, _ := cache.open(key, owner)
				if entry == nil { t.Error("one-key lease could not enter sparse cache"); return }
				cache.evictSlot(entry.slot)
				cache.close(entry, owner)
			}
		})
	}
	workers.Wait()
	cache.evictMatching(nil)
	if _, rows := cache.snapshot(); len(rows) != 0 { t.Fatal("joined leases retained occupied state", rows) }
}
