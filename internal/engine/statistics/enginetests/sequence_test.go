package statistics_test

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/statistics"
)

func TestStatsSequencePageArithmetic(t *testing.T) {
	for _, batches := range [][]int64{{}, {1}, {1, 1}, {2, 3}, {3, 2}, {1, 1, 1, 1, 1}} {
		t.Run(fmt.Sprint(batches), func(t *testing.T) {
			var p statistics.Provider
			x, _ := statsKeys()
			now := time.Unix(100, 0)
			var expected int64
			for _, n := range batches {
				var writer statistics.Writer
				writer.Add(&p, x, statistics.Fact{SequencePageAccesses: n})
				if !writer.Flush(&p, now, true) {
					t.Fatal("lost batch")
				}
				expected += n
			}
			fact := p.Read(x)
			if n := fact.SequencePageAccesses; n != expected {
				t.Fatal("nonadditive sequence encounters", n, expected)
			}
			fact.SequencePageAccesses = 0
			fact.ResetAt, fact.Generation = time.Time{}, 0
			if fact != (statistics.Fact{}) {
				t.Fatal("sequence encounters polluted unrelated families", fact)
			}
		})
	}
	// Concurrent first reservations merge into one encounter history. Projection
	// is independently asserted as (1,1) by the public two-session vector.
	var p statistics.Provider
	x, _ := statsKeys()
	one := statistics.Fact{SequencePageAccesses: 1}
	var workers sync.WaitGroup
	for range 2 {
		workers.Go(func() { var w statistics.Writer; w.Add(&p, x, one); w.Flush(&p, time.Unix(100, 0), true) })
	}
	workers.Wait()
	if n := p.Read(x).SequencePageAccesses; n != 2 {
		t.Fatal("concurrent first accesses", n)
	}
}

func TestStatsSequenceSnapshots(t *testing.T) {
	for _, mode := range []string{"none", "cache", "snapshot"} {
		for _, missing := range []bool{false, true} {
			t.Run(mode+"/missing="+map[bool]string{false: "false", true: "true"}[missing], func(t *testing.T) {
				var p statistics.Provider
				var w statistics.Writer
				var reader statistics.Reader
				a, b := statsKeys()
				now := time.Unix(100, 0)
				if !missing {
					w.Add(&p, a, statistics.Fact{SequencePageAccesses: 1})
					w.Add(&p, b, statistics.Fact{SequencePageAccesses: 2})
					w.Flush(&p, now, true)
				}
				reader.Configure(mode, 1)
				first := int64(1)
				if missing {
					first = 0
				}
				retained := reader.Read(&p, a, now)
				if n := retained.SequencePageAccesses; n != first {
					t.Fatal("initial A", n, first)
				}
				if (mode == "snapshot") != !reader.Timestamp().IsZero() {
					t.Fatal("lazy timestamp mode", reader.Timestamp())
				}
				w.Add(&p, a, statistics.Fact{SequencePageAccesses: 1})
				w.Add(&p, b, statistics.Fact{SequencePageAccesses: 1})
				w.Flush(&p, now.Add(time.Second), true)
				wantA, wantB := int64(1), int64(3)
				if missing {
					wantA, wantB = 0, 1
				}
				if mode == "none" {
					wantA++
				}
				if mode == "snapshot" {
					wantB--
					if missing {
						wantB = 0
					}
				}
				if n := reader.Read(&p, a, now).SequencePageAccesses; n != wantA {
					t.Fatal("A cache granularity", n, wantA)
				}
				if n := reader.Read(&p, b, now).SequencePageAccesses; n != wantB {
					t.Fatal("untouched B coherent capture", n, wantB)
				}
				w.Add(&p, a, statistics.Fact{SequencePageAccesses: 7})
				reader.Clear()
				if !reader.Timestamp().IsZero() {
					t.Fatal("clear retained timestamp")
				}
				published := int64(2)
				if missing {
					published = 1
				}
				if n := reader.Read(&p, a, now).SequencePageAccesses; n != published {
					t.Fatal("clear flushed pending", n, published)
				}
				if n := retained.SequencePageAccesses; n != first {
					t.Fatal("retained fact mutated", n)
				}
				reader.Configure("snapshot", 2)
				reader.Read(&p, a, now)
				reader.Configure("snapshot", 4)
				if !reader.Timestamp().IsZero() {
					t.Fatal("effective settings generation retained capture")
				}
			})
		}
	}
}

func TestStatsSequenceLifecycle(t *testing.T) {
	var p statistics.Provider
	var writer, late statistics.Writer
	x, temp := statsKeys()
	now := time.Unix(100, 0)
	writer.Add(&p, x, statistics.Fact{SequencePageAccesses: 2})
	writer.Add(&p, temp, statistics.Fact{SequencePageAccesses: 3})
	writer.Flush(&p, now, true)
	late.Add(&p, x, statistics.Fact{SequencePageAccesses: 11})
	p.Retire(x)
	late.Flush(&p, now, true)
	late.Add(&p, x, statistics.Fact{SequencePageAccesses: 13})
	late.Flush(&p, now, true)
	if n := p.Read(x).SequencePageAccesses; n != 0 {
		t.Fatal("retired sequence resurrected", n)
	}
	if n := p.Read(temp).SequencePageAccesses; n != 3 {
		t.Fatal("retirement affected peer", n)
	}
	writer.Add(&p, temp, statistics.Fact{SequencePageAccesses: 7})
	p.Reset(now.Add(time.Second))
	writer.Flush(&p, now.Add(time.Second), true)
	if n := p.Read(temp).SequencePageAccesses; n != 0 {
		t.Fatal("reset leaked pending generation", n)
	}
	writer.Add(&p, temp, statistics.Fact{SequencePageAccesses: 1})
	writer.Flush(&p, now, true)
	next := temp
	next.Object.Epoch++
	foreign := temp
	foreign.Object = (&ids.Domain{}).Ref(temp.Object.Epoch, temp.Object.Address)
	if p.Read(next).SequencePageAccesses != 0 || p.Read(foreign).SequencePageAccesses != 0 {
		t.Fatal("sequence domain/epoch alias")
	}
	writer.Add(&p, temp, statistics.Fact{SequencePageAccesses: 17})
	preserved := p.Read(temp)
	p.Replace(now.Add(2*time.Second), map[statistics.Key]statistics.Fact{next: preserved})
	writer.Flush(&p, now.Add(2*time.Second), true)
	if p.Read(next).SequencePageAccesses != 1 || p.Read(temp).SequencePageAccesses != 0 {
		t.Fatal("replacement retained pending or old epoch")
	}
	var fresh statistics.Reader
	fresh.Configure("snapshot", 1)
	if fresh.Read(&p, x, now).SequencePageAccesses != 0 {
		t.Fatal("replacement leaked retired history")
	}
}
