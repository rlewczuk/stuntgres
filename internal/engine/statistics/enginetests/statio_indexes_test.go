package statistics_test

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/statistics"
	"testing"
	"time"
)

func TestStatioAllIndexesProvider(t *testing.T) {
	var p statistics.Provider
	domain := new(ids.Domain)
	key := statistics.Key{Object: domain.Ref(1, ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 17000}), Category: statistics.Relation}
	other := statistics.Key{Object: domain.Ref(1, ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 17001}), Category: statistics.Relation}
	var w statistics.Writer
	on := statistics.Policy{Counts: true}
	w.IndexBlock(&p, key, statistics.Policy{IOTiming: true}, false)
	if w.Pending() {
		t.Fatal("disabled count allocated pending fact")
	}
	w.Tuple(&p, key, on, 3, 0, 0)
	w.Add(&p, key, statistics.Fact{BlocksFetched: 5, BlocksHit: 4})
	if fetched, hit := w.PendingBlocks(&p, key); fetched != 5 || hit != 4 {
		t.Fatal("generic pending request pair", fetched, hit)
	}
	w.Flush(&p, time.Unix(0, 0), true)
	if f := p.Read(key); f.IndexBlocksRead != 0 || f.IndexBlocksHit != 0 || f.BlocksFetched != 5 || f.BlocksHit != 4 {
		t.Fatal("generic requests leaked into index cache counters", f)
	}
	w.IndexBlock(&p, key, on, false)
	w.IndexBlock(&p, key, on, true)
	w.IndexBlock(&p, key, statistics.Policy{Counts: true, IOTiming: true}, true)
	w.Flush(&p, time.Unix(1, 0), true)
	f := p.Read(key)
	if f.IndexBlocksRead != 1 || f.IndexBlocksHit != 2 || f.Inserts != 3 || f.BlocksFetched != 5 || f.BlocksHit != 4 {
		t.Fatal("additive shared fact", f)
	}
	var reader statistics.Reader
	reader.Configure("snapshot", 1)
	retained := reader.Read(&p, key, time.Unix(2, 0))
	w.IndexBlock(&p, key, on, true)
	w.Flush(&p, time.Unix(3, 0), true)
	if retained.IndexBlocksHit != 2 || reader.Read(&p, key, time.Time{}).IndexBlocksHit != 2 {
		t.Fatal("reader borrowed mutable fact")
	}
	reader.Clear()
	if reader.Read(&p, key, time.Unix(4, 0)).IndexBlocksHit != 3 {
		t.Fatal("clear did not refresh")
	}
	if f := p.Read(key); f.BlocksFetched != 5 || f.BlocksHit != 4 {
		t.Fatal("index cache outcomes changed generic requests", f)
	}
	w.IndexBlock(&p, key, on, false)
	p.Reset(time.Unix(5, 0))
	w.Flush(&p, time.Unix(6, 0), true)
	if f := p.Read(key); f.IndexBlocksRead != 0 || f.IndexBlocksHit != 0 {
		t.Fatal("old generation resurrected", f)
	}
	w.IndexBlock(&p, key, on, false)
	w.IndexBlock(&p, other, on, true)
	p.Retire(key)
	w.Flush(&p, time.Unix(7, 0), true)
	w.IndexBlock(&p, key, on, true)
	w.Flush(&p, time.Unix(8, 0), true)
	if f := p.Read(key); f.IndexBlocksRead != 0 || f.IndexBlocksHit != 0 {
		t.Fatal("late retired key resurrected", f)
	}
	if p.Read(other).IndexBlocksHit != 1 {
		t.Fatal("retirement touched unrelated key")
	}
}
