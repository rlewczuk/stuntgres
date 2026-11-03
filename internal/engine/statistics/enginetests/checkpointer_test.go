package statistics_test

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"stuntgres/internal/engine/statistics"
)

func TestStatsCheckpointerProvider(t *testing.T) {
	at, next := time.Unix(100, 0).UTC(), time.Unix(200, 0).UTC()
	seed := statistics.SharedFact{Counters: [8]int64{11, 13, 17, 19, 23, 29, 31, 37}, WriteTime: 1250 * time.Microsecond, SyncTime: 2750 * time.Microsecond, TotalTime: 9 * time.Second}
	delta := statistics.SharedFact{Counters: [8]int64{1, 2, 3, 4, 5, 6, 7, 8}, WriteTime: 500 * time.Microsecond, SyncTime: 250 * time.Microsecond, ResetAt: next}
	for _, mode := range []string{"none", "cache", "snapshot"} {
		t.Run(mode, func(t *testing.T) {
			var p statistics.Provider
			if got := p.ReadShared(statistics.Checkpointer); got != (statistics.SharedFact{}) {
				t.Fatal("zero provider manufactured metadata", got)
			}
			p.ResetShared(statistics.Checkpointer, at)
			var w statistics.Writer
			w.Shared(&p, statistics.Checkpointer, seed)
			w.Shared(&p, statistics.Archiver, statistics.SharedFact{Counters: [8]int64{97}})
			w.Flush(&p, at, true)
			first := seed
			first.ResetAt = at
			if got := p.ReadShared(statistics.Checkpointer); got != first {
				t.Fatal("all seed fields", got, first)
			}
			other := p.ReadShared(statistics.Archiver)
			var r statistics.Reader
			r.Configure(mode, 1)
			if got := r.ReadShared(&p, statistics.Checkpointer, at); got != first {
				t.Fatal("initial full fact", got)
			}
			var firstOther statistics.Reader
			firstOther.Configure("snapshot", 1)
			firstOther.ReadShared(&p, statistics.Archiver, at)
			w.Shared(&p, statistics.Checkpointer, delta)
			w.Flush(&p, at.Add(time.Second), true)
			merged := first
			merged.Counters = [8]int64{12, 15, 20, 23, 28, 35, 38, 45}
			merged.WriteTime, merged.SyncTime = 1750*time.Microsecond, 3*time.Millisecond
			if got := p.ReadShared(statistics.Checkpointer); got != merged {
				t.Fatal("independent additive fields; pending reset ignored", got, merged)
			}
			if got := firstOther.ReadShared(&p, statistics.Checkpointer, next); got != first {
				t.Fatal("another family's all-kind cut lost full checkpointer vector", got, first)
			}
			want := first
			if mode == "none" {
				want = merged
			}
			if got := r.ReadShared(&p, statistics.Checkpointer, next); got != want {
				t.Fatal("retained full vector", got, want)
			}
			// Published reset neither drains pending deltas nor changes throttle.
			w.Shared(&p, statistics.Checkpointer, delta)
			owner := reflect.ValueOf(&w).Elem()
			before := fmtStatsWriter(owner)
			p.ResetShared(statistics.Checkpointer, next)
			if got := p.ReadShared(statistics.Checkpointer); got != (statistics.SharedFact{ResetAt: next}) {
				t.Fatal("selected complete zeroing", got)
			}
			if !w.Pending() || fmtStatsWriter(owner) != before {
				t.Fatal("reset changed pending writer ownership")
			}
			if p.ReadShared(statistics.Archiver) != other {
				t.Fatal("unrelated family changed")
			}
			if mode != "none" && r.ReadShared(&p, statistics.Checkpointer, next) != first {
				t.Fatal("reset mutated old cut")
			}
			r.Clear()
			if got := r.ReadShared(&p, statistics.Checkpointer, next); got != (statistics.SharedFact{ResetAt: next}) {
				t.Fatal("clear full vector", got)
			}
			w.Flush(&p, next, true)
			pending := delta
			pending.ResetAt = next
			if got := p.ReadShared(statistics.Checkpointer); got != pending {
				t.Fatal("pending survives reset", got, pending)
			}
			p.ResetAllShared(next.Add(time.Second))
			if got := p.ReadShared(statistics.Checkpointer); got != (statistics.SharedFact{ResetAt: next.Add(time.Second)}) {
				t.Fatal("all complete zeroing", got)
			}
			w.Shared(&p, statistics.Checkpointer, seed)
			p.Replace(next, nil)
			w.Flush(&p, next.Add(time.Second), true)
			if got := p.ReadShared(statistics.Checkpointer); got != (statistics.SharedFact{ResetAt: next}) {
				t.Fatal("old generation publication survived replacement", got)
			}
		})
	}
	t.Run("coherent concurrent vector", func(t *testing.T) {
		var p statistics.Provider
		p.ResetShared(statistics.Checkpointer, at)
		start, done := make(chan struct{}), make(chan struct{})
		go func() {
			defer close(done)
			<-start
			var w statistics.Writer
			for range 200 {
				w.Shared(&p, statistics.Checkpointer, seed)
				w.Flush(&p, at, true)
				p.ResetShared(statistics.Checkpointer, at)
			}
		}()
		close(start)
		positive := seed
		positive.ResetAt = at
		var invalid statistics.SharedFact
		for range 200 {
			got := p.ReadShared(statistics.Checkpointer)
			if got != positive && got != (statistics.SharedFact{ResetAt: at}) {
				invalid = got
			}
		}
		<-done
		if invalid != (statistics.SharedFact{}) {
			t.Fatal("torn eight-counter/two-time fact", invalid)
		}
	})
}

// Includes every private pending field and the actual throttle/generation state.
func fmtStatsWriter(v reflect.Value) string {
	return fmt.Sprintf("%#v", v)
}
