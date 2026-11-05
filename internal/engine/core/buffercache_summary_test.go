package core

import (
	"testing"

	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/util"
)

func summaryLeafKey(domain *ids.Domain, n ids.OID) logicalBufferKey {
	return logicalBufferKey{domain: domain, epoch: 1, relation: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: n}, node: n, space: 1663, database: 5}
}
func summaryLeafAssert(t *testing.T, got, want functions.PgBuffercacheCounts) {
	t.Helper()
	if got != want { t.Fatal("detached shared counts", got, "want", want) }
	var buffers, dirty, pinned int64
	for _, bucket := range got.Buckets {
		if bucket.Dirty > bucket.Buffers || bucket.Pinned > bucket.Buffers { t.Fatal("bucket bounds", got) }
		buffers += bucket.Buffers
		dirty += bucket.Dirty
		pinned += bucket.Pinned
	}
	if buffers != got.Capacity || dirty != got.Dirty || pinned != got.Pinned || got.Dirty > got.Used || got.Pinned > got.Used { t.Fatal("capacity/used bounds", got) }
}

func TestPgBuffercacheSummaryAggregation(t *testing.T) {
	domain := new(ids.Domain)
	a, b := domain.Session(1), domain.Session(2)
	for _, capacity := range []int64{0, 1, 3, 1<<40} {
		cache := &logicalBufferCache{capacity: capacity}
		want := functions.PgBuffercacheCounts{Capacity: capacity}
		want.Buckets[0].Buffers = capacity
		summaryLeafAssert(t, cache.aggregate(), want)
		if capacity == 0 { continue }
		key := summaryLeafKey(domain, 17000)
		first, _ := cache.open(key, a)
		cache.open(key, a) // Alias contributes one pinned buffer, not two.
		cache.open(key, b) // A second session still contributes one pinned buffer.
		want.Used, want.Pinned, want.UsageTotal = 1, 1, 3
		want.Buckets[0].Buffers = capacity-1
		want.Buckets[3] = functions.PgBuffercacheBucket{Buffers: 1, Pinned: 1}
		summaryLeafAssert(t, cache.aggregate(), want)
		cache.close(first, a)
		cache.close(first, b)
		summaryLeafAssert(t, cache.aggregate(), want)
		cache.close(first, a)
		want.Pinned, want.Buckets[3].Pinned = 0, 0
		summaryLeafAssert(t, cache.aggregate(), want)
		// The owner can contain a valid usage-zero entry; it is used, not unused.
		cache.mu.Lock()
		first.usage = 0
		cache.mu.Unlock()
		want.UsageTotal = 0
		want.Buckets[3] = functions.PgBuffercacheBucket{}
		want.Buckets[0].Buffers = capacity
		summaryLeafAssert(t, cache.aggregate(), want)
		for range 8 { cache.touch(first) }
		want.UsageTotal = 5
		want.Buckets[0].Buffers = capacity-1
		want.Buckets[5].Buffers = 1
		summaryLeafAssert(t, cache.aggregate(), want)
		detached := cache.aggregate()
		if !cache.evictSlot(first.slot) { t.Fatal("settled entry did not retire") }
		key.epoch++
		fresh, _ := cache.open(key, b)
		cache.close(first, b) // Late old-generation release cannot change fresh pins.
		want.UsageTotal, want.Pinned = 1, 1
		want.Buckets[5] = functions.PgBuffercacheBucket{}
		want.Buckets[1] = functions.PgBuffercacheBucket{Buffers: 1, Pinned: 1}
		summaryLeafAssert(t, cache.aggregate(), want)
		if detached.UsageTotal != 5 || detached.Pinned != 0 { t.Fatal("observation retained mutable owner") }
		if capacity > 1 {
			other, _ := cache.open(summaryLeafKey(domain, 17001), a)
			want.Used, want.Pinned, want.UsageTotal = 2, 2, 2
			want.Buckets[0].Buffers = capacity-2
			want.Buckets[1] = functions.PgBuffercacheBucket{Buffers: 2, Pinned: 2}
			summaryLeafAssert(t, cache.aggregate(), want)
			cache.close(other, a)
			cache.evictSlot(other.slot) // Sparse hole is part of bucket zero.
			want.Used, want.Pinned, want.UsageTotal = 1, 1, 1
			want.Buckets[0].Buffers = capacity-1
			want.Buckets[1] = functions.PgBuffercacheBucket{Buffers: 1, Pinned: 1}
			summaryLeafAssert(t, cache.aggregate(), want)
		}
		cache.close(fresh, b)
	}
	// Full tiny cache: every slot is occupied, including a valid usage zero.
	cache := &logicalBufferCache{capacity: 2}
	first, _ := cache.open(summaryLeafKey(domain, 1), a)
	second, _ := cache.open(summaryLeafKey(domain, 2), b)
	cache.mu.Lock()
	first.usage, second.usage = 0, 5
	cache.mu.Unlock()
	want := functions.PgBuffercacheCounts{Capacity: 2, Used: 2, Pinned: 2, UsageTotal: 5}
	want.Buckets[0] = functions.PgBuffercacheBucket{Buffers: 1, Pinned: 1}
	want.Buckets[5] = functions.PgBuffercacheBucket{Buffers: 1, Pinned: 1}
	summaryLeafAssert(t, cache.aggregate(), want)
}

var summaryAllocationSink functions.PgBuffercacheCounts

func TestPgBuffercacheSummaryAggregationAllocations(t *testing.T) {
	domain := new(ids.Domain)
	for _, populated := range []bool{false, true} {
		cache := &logicalBufferCache{capacity: 1<<40}
		if populated {
			for i := ids.OID(1); i <= 4; i++ { cache.open(summaryLeafKey(domain, i), domain.Session(ids.SessionID(i))) }
		}
		allocations, bytes := util.AllocationsPerCall(1000, func() { summaryAllocationSink = cache.aggregate() })
		if allocations != 0 || bytes != 0 { t.Fatal("fixed sparse aggregate allocated", populated, allocations, bytes) }
		if summaryAllocationSink.Capacity != 1<<40 || populated && summaryAllocationSink.Used != 4 { t.Fatal("allocation witness did not observe real sparse owner", summaryAllocationSink) }
	}
}
