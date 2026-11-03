package statistics_test

import (
	"sync"
	"testing"
	"time"

	"stuntgres/internal/engine/statistics"
)

func TestStatsSLRUProvider(t *testing.T) {
	first := [7]int64{11, 23, 37, 41, 53, 67, 79}
	second := [7]int64{2, 3, 5, 7, 11, 13, 17}
	t0, t1 := time.Unix(100, 0).UTC(), time.Unix(200, 0).UTC()
	for slot, n := range first {
		var p statistics.Provider
		p.ResetShared(statistics.SLRU, t0)
		var w statistics.Writer
		var one [7]int64
		one[slot] = n
		w.SLRU(&p, statistics.Transaction, statistics.SLRUFact{Counters: one, ResetAt: t1})
		w.Flush(&p, t0, true)
		var r statistics.Reader
		r.Configure("none", 1)
		if got := r.ReadSLRUs(&p, t1)[statistics.Transaction]; got != (statistics.SLRUFact{Counters: one, ResetAt: t0}) {
			t.Fatal("one-hot publication must preserve reset authority", slot, got)
		}
	}
	for _, mode := range []string{"cache", "snapshot"} {
		var p statistics.Provider
		p.ResetShared(statistics.SLRU, t0)
		var w statistics.Writer
		for k := statistics.SLRUKind(0); k < statistics.SLRUCount; k++ {
			w.SLRU(&p, k, statistics.SLRUFact{Counters: first})
		}
		w.Flush(&p, t0, true)
		var r statistics.Reader
		r.Configure(mode, 1)
		if r.ReadSLRU(&p, statistics.Transaction, t0).Counters != first { t.Fatal("first-entry seed") }
		p.ResetShared(statistics.SLRU, t1)
		for _, got := range r.ReadSLRUs(&p, t1) {
			if got != (statistics.SLRUFact{Counters: first, ResetAt: t0}) { t.Fatal("first entry must capture siblings", mode, got) }
		}
		if r.Timestamp().IsZero() != (mode != "snapshot") { t.Fatal("mode timestamp", mode, r.Timestamp()) }
		r.Clear()
		for _, got := range r.ReadSLRUs(&p, t1) {
			if got != (statistics.SLRUFact{ResetAt: t1}) { t.Fatal("clear must release family", mode, got) }
		}
	}
	var p statistics.Provider
	p.ResetShared(statistics.SLRU, t0)
	var w statistics.Writer
	for k := statistics.SLRUKind(0); k < statistics.SLRUCount; k++ {
		w.SLRU(&p, k, statistics.SLRUFact{Counters: first})
	}
	w.Flush(&p, t0, true)
	w.SLRU(&p, statistics.Transaction, statistics.SLRUFact{Counters: second})
	w.Flush(&p, t0, true)
	if got := p.ReadSLRU(statistics.Transaction); got != (statistics.SLRUFact{Counters: [7]int64{13, 26, 42, 48, 64, 80, 96}, ResetAt: t0}) {
		t.Fatal("seven-slot addition", got)
	}
	peer := p.ReadSLRU(statistics.OtherSLRU)
	w.SLRU(&p, statistics.Transaction, statistics.SLRUFact{Counters: second})
	generation := p.Generation()
	p.ResetSLRU(statistics.Transaction, t1)
	if p.ReadSLRU(statistics.Transaction) != (statistics.SLRUFact{ResetAt: t1}) || p.ReadSLRU(statistics.OtherSLRU) != peer || p.Generation() != generation {
		t.Fatal("selected reset isolation")
	}
	w.Flush(&p, t1, true)
	if got := p.ReadSLRU(statistics.Transaction); got != (statistics.SLRUFact{Counters: second, ResetAt: t1}) {
		t.Fatal("pending reports survive published reset", got)
	}
	before := p.ReadSLRUs()
	w.SLRU(&p, statistics.SLRUCount, statistics.SLRUFact{Counters: first})
	p.ResetSLRU(statistics.SLRUCount, t0)
	if p.ReadSLRUs() != before || p.ReadSLRU(statistics.SLRUCount) != (statistics.SLRUFact{}) {
		t.Fatal("invalid kind changed family")
	}
	p.ResetShared(statistics.SLRU, t1)
	for _, got := range p.ReadSLRUs() {
		if got != (statistics.SLRUFact{ResetAt: t1}) { t.Fatal("all reset", got) }
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := range 1000 { p.ResetShared(statistics.SLRU, t0.Add(time.Duration(i)*time.Microsecond)) }
	}()
	for range 1000 {
		family := p.ReadSLRUs()
		for _, got := range family {
			if got != family[0] { t.Error("family capture tore all-reset", family); break }
		}
	}
	wg.Wait()
}
