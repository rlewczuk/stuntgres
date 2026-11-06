package core

import (
	"stuntgres/internal/engine/ids"
	"sync"
	"sync/atomic"
	"testing"
)

func TestStatioAllIndexesCacheOutcomes(t *testing.T) {
	domain := new(ids.Domain)
	owner := domain.Session(1)
	key := func(n ids.OID) logicalBufferKey {
		return logicalBufferKey{logicalBufferIncarnation: logicalBufferIncarnation{domain: domain, epoch: 1, relation: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: n}, node: n, database: 5, space: 1663}}
	}
	cache := &logicalBufferCache{capacity: 2}
	a, hit := cache.open(key(17000), owner)
	if a == nil || hit {
		t.Fatal("cold A", a, hit)
	}
	for range 9 {
		if !cache.touch(a) {
			t.Fatal("already-open resident miss")
		}
	}
	if a.usage != 5 || a.owners[owner] != 1 {
		t.Fatal("touch changed pin or failed saturation", a)
	}
	b, hit := cache.open(key(17001), owner)
	if b == nil || hit {
		t.Fatal("cold B", b, hit)
	}
	for range 2 {
		if c, hit := cache.open(key(17002), owner); c != nil || hit {
			t.Fatal("all-pinned attempt invented hit", c, hit)
		}
	}
	cache.close(a, owner)
	c, hit := cache.open(key(17002), owner)
	if c == nil || hit || c.slot != 1 {
		t.Fatal("retry/eviction", c, hit)
	}
	if cache.touch(a) {
		t.Fatal("retired entry touched reused slot")
	}
	cache.close(c, owner)
	a, hit = cache.open(key(17000), owner)
	if a == nil || hit {
		t.Fatal("evicted A not cold", a, hit)
	}
	cache.close(a, owner)
	a, hit = cache.open(key(17000), owner)
	if a == nil || !hit {
		t.Fatal("warm A", a, hit)
	}
	cache.close(a, owner)
	cache.close(b, owner)
	zero := &logicalBufferCache{}
	if e, hit := zero.open(key(17003), owner); e != nil || hit {
		t.Fatal("zero capacity invented residency", e, hit)
	}
	t.Run("concurrent_residency", func(t *testing.T) {
		shared := &logicalBufferCache{capacity: 1}
		var workers sync.WaitGroup
		var reads, hits atomic.Int64
		for i := range 8 {
			token := domain.Session(ids.SessionID(i + 2))
			workers.Go(func() {
				for range 8 {
					entry, resident := shared.open(key(17004), token)
					if resident {
						hits.Add(1)
					} else {
						reads.Add(1)
					}
					shared.close(entry, token)
				}
			})
		}
		workers.Wait()
		if reads.Load() != 1 || hits.Load() != 63 {
			t.Fatal("atomic pre-access outcomes", reads.Load(), hits.Load())
		}
		_, rows := shared.snapshot()
		if rows[1].pins != 0 || rows[1].usage != 5 {
			t.Fatal("concurrent ownership leak", rows)
		}
	})
}
