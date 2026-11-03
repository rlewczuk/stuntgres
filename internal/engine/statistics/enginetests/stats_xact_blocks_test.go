package statistics_test

import (
	"testing"
	"time"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/util"
)

func TestStatsXactBlocksProvider(t *testing.T) {
	var p statistics.Provider
	var writer statistics.Writer
	a, b := statsKeys()
	now := time.Unix(100, 0)
	pending := func(key statistics.Key, fetched, hit int64) {
		t.Helper()
		f, h := writer.PendingBlocks(&p, key)
		if f != fetched || h != hit {
			t.Fatalf("pending %+v = %d/%d want %d/%d", key, f, h, fetched, hit)
		}
	}
	published := func(key statistics.Key, fetched, hit int64) {
		t.Helper()
		f := p.Read(key)
		if f.BlocksFetched != fetched || f.BlocksHit != hit {
			t.Fatalf("published %+v = %+v want %d/%d", key, f, fetched, hit)
		}
	}
	pending(a, 0, 0)
	if writer.Pending() {
		t.Fatal("lookup allocated pending work")
	}
	allocations, bytes := util.AllocationsPerCall(100, func() { writer.PendingBlocks(&p, a) })
	if allocations != 0 || bytes != 0 {
		t.Fatal("missing lookup allocated", allocations, bytes)
	}
	writer.Add(&p, a, statistics.Fact{BlocksFetched: 1, BlocksHit: 1})
	pending(a, 1, 1)
	published(a, 0, 0)
	writer.Add(&p, a, statistics.Fact{BlocksFetched: 2, BlocksHit: 2})
	pending(a, 3, 3)
	writer.Add(&p, b, statistics.Fact{BlocksFetched: 7, BlocksHit: 5})
	otherCategory := a
	otherCategory.Category = statistics.Function
	writer.Add(&p, otherCategory, statistics.Fact{Calls: 11})
	pending(otherCategory, 0, 0)
	foreign, next := a, a
	foreign.Object = (&ids.Domain{}).Ref(a.Object.Epoch, a.Object.Address)
	next.Object.Epoch++
	pending(foreign, 0, 0)
	pending(next, 0, 0)
	if !writer.Flush(&p, now, false) {
		t.Fatal("lookup moved first publication frontier")
	}
	pending(a, 0, 0)
	published(a, 3, 3)
	published(b, 7, 5)
	if p.Read(otherCategory).Calls != 11 {
		t.Fatal("relation publication damaged function fact")
	}
	writer.Add(&p, a, statistics.Fact{BlocksFetched: 1, BlocksHit: 1})
	var reader statistics.Reader
	reader.Configure("snapshot", 1)
	reader.Read(&p, a, now)
	reader.Clear()
	pending(a, 1, 1)
	allocations, bytes = util.AllocationsPerCall(100, func() { writer.PendingBlocks(&p, a) })
	if allocations != 0 || bytes != 0 {
		t.Fatal("populated lookup allocated", allocations, bytes)
	}
	if writer.Flush(&p, now.Add(999*time.Millisecond), false) {
		t.Fatal("lookup moved throttle or flushed early")
	}
	pending(a, 1, 1)
	if !writer.Flush(&p, now.Add(time.Second), false) {
		t.Fatal("eligible publication rejected")
	}
	pending(a, 0, 0)
	published(a, 4, 4)
	writer.Add(&p, a, statistics.Fact{BlocksFetched: 13, BlocksHit: 9})
	p.ResetShared(statistics.SharedIO, now)
	pending(a, 13, 9)
	p.Retire(a)
	pending(a, 0, 0)
	writer.Add(&p, a, statistics.Fact{BlocksFetched: 17, BlocksHit: 17})
	writer.Flush(&p, now, true)
	pending(a, 0, 0)
	published(a, 0, 0)
	published(b, 7, 5)
	writer.Add(&p, b, statistics.Fact{BlocksFetched: 19, BlocksHit: 19})
	generation := p.Generation()
	p.Reset(now)
	if p.Generation() == generation {
		t.Fatal("reset did not fence generation")
	}
	pending(b, 0, 0)
	writer.Flush(&p, now, true)
	published(b, 0, 0)
	// Reset preserves retirement, while fresh Add on a live key reinitializes
	// only this writer's generation rather than reviving the stale prefix.
	writer.Add(&p, a, statistics.Fact{BlocksFetched: 23, BlocksHit: 23})
	pending(a, 0, 0)
	writer.Add(&p, b, statistics.Fact{BlocksFetched: 1, BlocksHit: 1})
	pending(b, 1, 1)
	p.Replace(now, map[statistics.Key]statistics.Fact{next: {BlocksFetched: 29, BlocksHit: 27}})
	pending(b, 0, 0)
	writer.Flush(&p, now, true)
	published(b, 0, 0)
	published(next, 29, 27)
	writer.Add(&p, next, statistics.Fact{BlocksFetched: 1, BlocksHit: 1})
	pending(next, 1, 1)
	pending(a, 0, 0)
	pending(foreign, 0, 0)
}
