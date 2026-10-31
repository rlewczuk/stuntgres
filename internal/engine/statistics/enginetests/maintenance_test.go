package statistics_test

import (
	"reflect"
	"sync"
	"testing"
	"time"

	"stuntgres/internal/engine/statistics"
)

func TestStatsProviderMaintenanceReporting(t *testing.T) {
	t.Run("pending_generation", func(t *testing.T) {
		var p statistics.Provider
		var writer statistics.Writer
		key, _ := statsKeys()
		now := time.Now()
		writer.Add(&p, key, statistics.Fact{Dead: 9})
		p.Reset(now)
		if writer.PendingDead(&p, key) != 0 {
			t.Fatal("pre-reset pending dead adjusted a new report")
		}
		p.ReportAnalyze(key, statistics.Policy{Counts: true}, now, time.Millisecond, 10, 5-writer.PendingDead(&p, key), true)
		writer.Flush(&p, now, true)
		if f := p.Read(key); f.Dead != 5 || f.AnalyzeCount != 1 || f.ResetAt != now {
			t.Fatal("stale writer/report generation ordering", f)
		}
	})
	for _, kind := range []string{"vacuum", "analyze", "analyze_no_reset"} {
		t.Run(kind, func(t *testing.T) {
			var p statistics.Provider
			var writer, peer statistics.Writer
			x, y := statsKeys()
			now := time.Now()
			on := statistics.Policy{Counts: true}
			writer.Add(&p, x, statistics.Fact{Inserts: 5, Live: 5, Dead: 4, Modifications: 5, InsertsSince: 5})
			writer.Flush(&p, now, false)
			pending := statistics.Fact{Inserts: 2, Live: 2, Modifications: 2, InsertsSince: 2}
			if kind != "vacuum" {
				pending = statistics.Fact{Inserts: 2, Updates: 1, Deletes: 1, Live: 1, Dead: 2, Modifications: 4, InsertsSince: 2}
			}
			writer.Add(&p, x, pending)
			writer.Add(&p, y, statistics.Fact{Inserts: 9})
			var snapshot statistics.Reader
			snapshot.Configure("snapshot", 1)
			before := snapshot.Read(&p, x, now)
			if kind == "vacuum" {
				p.ReportVacuum(x, on, now, 7*time.Millisecond, 20, 0)
			} else {
				p.ReportAnalyze(x, on, now, 5*time.Millisecond, 18, 5, kind == "analyze")
			}
			got := p.Read(x)
			want := before
			if kind == "vacuum" {
				want.Live, want.Dead, want.InsertsSince = 20, 0, 0
				want.VacuumCount, want.LastVacuum, want.VacuumTime = 1, now, 7*time.Millisecond
			} else {
				want.Live, want.Dead = 18, 5
				if kind == "analyze" {
					want.Modifications = 0
				}
				want.AnalyzeCount, want.LastAnalyze, want.AnalyzeTime = 1, now, 5*time.Millisecond
			}
			if got != want {
				t.Fatal("immediate shared report", got, want)
			}
			if !writer.Pending() || writer.PendingDead(&p, x) != pending.Dead || p.Read(y).Inserts != 0 {
				t.Fatal("maintenance consumed pending or unrelated work")
			}
			if snapshot.Read(&p, x, now) != before {
				t.Fatal("maintenance invalidated reader snapshot")
			}
			snapshot.Clear()
			if snapshot.Read(&p, x, now) != want {
				t.Fatal("clear did not expose immediate report")
			}
			p.ReportAnalyze(x, statistics.Policy{}, now.Add(time.Second), time.Hour, 99, 99, true)
			p.ReportVacuum(x, statistics.Policy{}, now.Add(time.Second), time.Hour, 99, 99)
			if p.Read(x) != want {
				t.Fatal("suppressed maintenance changed shared state")
			}
			if writer.Flush(&p, now.Add(999*time.Millisecond), false) {
				t.Fatal("maintenance changed ordinary throttle")
			}
			peer.Add(&p, x, statistics.Fact{Inserts: 3, Live: 3, Modifications: 3, InsertsSince: 3})
			peer.Flush(&p, now, true)
			if got := p.Read(x); got.Inserts != 8 || got.Live != want.Live+3 || got.Modifications != want.Modifications+3 || got.InsertsSince != want.InsertsSince+3 {
				t.Fatal("intervening peer publication", got)
			}
			writer.Flush(&p, now.Add(time.Second), false)
			want.Inserts += 5
			want.Updates += pending.Updates
			want.Deletes += pending.Deletes
			want.Live += 3 + pending.Live
			want.Dead += pending.Dead
			want.Modifications += 3 + pending.Modifications
			want.InsertsSince += 3 + pending.InsertsSince
			if p.Read(x) != want {
				t.Fatal("later drain reordered maintenance/reset", p.Read(x), want)
			}
		})
	}
	t.Run("retired_preparing_writer", func(t *testing.T) {
		var p statistics.Provider
		var origin, finisher statistics.Writer
		x, y := statsKeys()
		now := time.Now()
		on := statistics.Policy{Counts: true}
		origin.Add(&p, x, statistics.Fact{Inserts: 2, Dead: 2, InsertsSince: 2})
		p.ReportAnalyze(x, on, now, time.Millisecond, 0, 0, true)
		p.ReportVacuum(y, on, now, time.Millisecond, 3, 0)
		peerBefore := p.Read(y)
		finisher.Retire(&p, x)
		origin.Flush(&p, now, true)
		origin.Add(&p, x, statistics.Fact{Inserts: 7})
		p.ReportAnalyze(x, on, now, time.Second, 7, 0, true)
		if p.Read(x).AnalyzeCount != 0 || p.Read(x).Inserts != 0 || origin.Pending() || p.Read(y) != peerBefore {
			t.Fatal("retired key resurrected or unrelated key changed")
		}
		p.Reset(now)
		origin.Add(&p, x, statistics.Fact{Inserts: 9})
		p.ReportVacuum(x, on, now, time.Second, 9, 0)
		if origin.Pending() || p.Read(x).VacuumCount != 0 {
			t.Fatal("statistics reset resurrected retired authority")
		}
	})
	t.Run("atomic_reports_and_snapshot", func(t *testing.T) {
		var p statistics.Provider
		x, _ := statsKeys()
		now := time.Now()
		on := statistics.Policy{Counts: true}
		var wg sync.WaitGroup
		for range 2 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for range 100 {
					p.ReportAnalyze(x, on, now, time.Millisecond, 7, 3, true)
				}
			}()
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			var r statistics.Reader
			r.Configure("snapshot", 0)
			for range 100 {
				r.Clear()
				f := r.Read(&p, x, now)
				if f.AnalyzeCount > 0 && (f.Live != 7 || f.Dead != 3 || f.AnalyzeTime != time.Duration(f.AnalyzeCount)*time.Millisecond || f.LastAnalyze != now) {
					t.Error("mixed report snapshot", f)
				}
			}
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			var peer statistics.Writer
			for range 100 {
				peer.Add(&p, x, statistics.Fact{Inserts: 1})
				peer.Flush(&p, now, true)
			}
		}()
		wg.Wait()
		if f := p.Read(x); f.AnalyzeCount != 200 || f.AnalyzeTime != 200*time.Millisecond || f.Inserts != 100 {
			t.Fatal("lost immediate reports or peer publication", f)
		}
	})
	t.Run("current_frame_query", func(t *testing.T) {
		x, _ := statsKeys()
		var owner, suffix statistics.Ledger
		owner.Record(x, statistics.Fact{Inserts: 20}, 0)
		owner.Record(x, statistics.Fact{Inserts: 30, Updates: 4}, 1)
		suffix.Record(x, statistics.Fact{Inserts: 90}, 1)
		suffix.Record(x, statistics.Fact{Truncated: true}, 1)
		suffix.Record(x, statistics.Fact{Inserts: 3, Updates: 2, Deletes: 1}, 1)
		suffix.Record(x, statistics.Fact{Inserts: 4, Deletes: 2}, 2)
		beforeOwner, beforeSuffix := owner.Clone(), suffix.Clone()
		if got := owner.CurrentCounts(x, suffix); got != (statistics.TupleCounts{Inserts: 27, Updates: 2, Deletes: 3}) {
			t.Fatal("same-level truncate/current parents", got)
		}
		if !reflect.DeepEqual(owner, beforeOwner) || !reflect.DeepEqual(suffix, beforeSuffix) {
			t.Fatal("estimate query changed ownership")
		}
	})
}
