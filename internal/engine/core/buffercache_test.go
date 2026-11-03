package core

import (
	"stuntgres/internal/engine/ids"
	"sync"
	"testing"
)

// These are pure leaf-owner units, not SQL-output scenarios. Keys use real
// domain-owned catalog addresses; no storage roots or providers are forged.
func TestPgBuffercacheCacheLifecycle(t *testing.T) {
	domain := new(ids.Domain)
	key := func(n ids.OID) logicalBufferKey {
		return logicalBufferKey{domain: domain, epoch: 1, relation: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: n}, node: n, space: 1663, database: 5}
	}
	owner := domain.Session(1)
	cache := &logicalBufferCache{capacity: 2}
	if cache.byKey != nil || cache.bySlot != nil {
		t.Fatal("cache construction allocated occupancy")
	}
	capacity, rows := cache.snapshot()
	if capacity != 2 || rows != nil || cache.byKey != nil {
		t.Fatal("empty snapshot allocated capacity-sized storage")
	}
	first, _ := cache.open(key(17000), owner)
	second, _ := cache.open(key(17001), owner)
	if first.slot != 1 || second.slot != 2 {
		t.Fatal("lowest free slots", first.slot, second.slot)
	}
	if entry, _ := cache.open(key(17002), owner); entry != nil {
		t.Fatal("all-pinned cache evicted a live owner")
	}
	_, retained := cache.snapshot()
	cache.close(first, owner)
	third, _ := cache.open(key(17002), owner)
	if third.slot != 1 || cache.byKey[first.key] != nil || cache.bySlot[2] != second {
		t.Fatal("deterministic unpinned replacement")
	}
	cache.close(first, owner)
	if len(third.owners) != 1 {
		t.Fatal("late old-slot close released replacement owner")
	}
	if retained[1].node != 17000 || retained[1].pins != 1 {
		t.Fatal("snapshot borrowed mutable occupancy", retained)
	}
	for range 9 {
		cache.touch(third)
	}
	if third.usage != 5 {
		t.Fatal("read usage did not saturate", third.usage)
	}
	cache.close(third, owner)
	cache.close(second, owner)
	cache.mu.Lock()
	cache.retireLocked(third.key, third)
	cache.mu.Unlock()
	fourth, _ := cache.open(key(17003), owner)
	if fourth.slot != 1 || fourth.usage != 1 {
		t.Fatal("retired lowest hole not reused cold", fourth)
	}
	cache.mu.Lock()
	cache.retireLocked(fourth.key, fourth)
	cache.mu.Unlock()
	if fourth.owners != nil {
		t.Fatal("retirement retained session tokens")
	}
	cache.close(fourth, owner)
	cache.close(fourth, owner)
}

func TestPgBuffercacheCacheSessionOwnership(t *testing.T) {
	cache := &logicalBufferCache{capacity: 1}
	key := logicalBufferKey{domain: new(ids.Domain), epoch: 1, relation: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 17000}, node: 17000, space: 1663, database: 5}
	owner, peer := key.domain.Session(1), key.domain.Session(2)
	first, _ := cache.open(key, owner)
	second, _ := cache.open(key, owner)
	if first != second || len(first.owners) != 1 || first.owners[owner] != 2 || first.usage != 2 {
		t.Fatal("aliases counted as different backends")
	}
	third, _ := cache.open(key, peer)
	if third != first || len(first.owners) != 2 {
		t.Fatal("distinct sessions were merged")
	}
	cache.close(third, peer)
	cache.close(first, owner)
	if len(first.owners) != 1 {
		t.Fatal("one alias close released all ownership")
	}
	cache.close(second, owner)
	cache.close(second, owner)
	if len(first.owners) != 0 {
		t.Fatal("idempotent terminal release retained owner")
	}
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() {
			for range 8 {
				entry, _ := cache.open(key, owner)
				cache.close(entry, owner)
			}
		})
	}
	workers.Wait()
	_, snapshot := cache.snapshot()
	if snapshot[1].pins != 0 || snapshot[1].usage != 5 {
		t.Fatal("concurrent leaf ownership", snapshot)
	}
}
