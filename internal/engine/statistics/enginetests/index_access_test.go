package statistics_test

import (
	"stuntgres/internal/engine/statistics"
	"testing"
	"time"
)

func TestStatsProviderIndexAccess(t *testing.T) {
	a := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	b := a.Add(time.Second)
	c := b.Add(time.Second)
	d := c.Add(time.Second)
	var p statistics.Provider
	var w statistics.Writer
	x, y := statsKeys()
	enabled := statistics.Policy{Counts: true}
	check := func(scans, entries, fetches int64, last time.Time) {
		t.Helper()
		f := p.Read(x)
		if f.IndexScans != scans || f.IndexEntries != entries || f.IndexFetches != fetches || !f.LastIndexScan.Equal(last) {
			t.Fatalf("index fact %+v want %d/%d/%d/%v", f, scans, entries, fetches, last)
		}
	}
	w.IndexAccess(&p, x, statistics.Policy{}, 1, 2, 2)
	if w.Pending() {
		t.Fatal("disabled event allocated pending work")
	}
	w.IndexAccess(&p, x, enabled, 1, 2, 2)
	w.IndexAccess(&p, x, enabled, 1, 0, 0)
	w.Tuple(&p, x, enabled, 7, 3, 1)
	w.TransactionStopped(b)
	if !w.Flush(&p, c, false) {
		t.Fatal("first flush")
	}
	check(2, 2, 2, b)
	retained := p.Read(x)
	w.IndexAccess(&p, x, enabled, 0, 3, 0)
	w.TransactionStopped(c)
	w.Flush(&p, c, true)
	check(2, 5, 2, b)
	w.IndexAccess(&p, x, enabled, 1, 0, 0)
	if w.Flush(&p, c.Add(time.Millisecond), false) {
		t.Fatal("throttle bypass")
	}
	check(2, 5, 2, b)
	w.TransactionStopped(d) // Later completed transaction did not scan.
	if !w.Flush(&p, d.Add(time.Second), false) {
		t.Fatal("due batch")
	}
	check(3, 5, 2, d)
	if f := p.Read(x); f.Inserts != 7 || f.Updates != 3 || f.Deletes != 1 {
		t.Fatal("relation merge lost tuple fields", f)
	}
	w.TransactionStopped(d.Add(time.Second))
	if w.Flush(&p, d.Add(2*time.Second), true) {
		t.Fatal("empty flush")
	}
	check(3, 5, 2, d)
	w.Add(&p, x, statistics.Fact{LastIndexScan: d.Add(time.Hour), IndexEntries: 2, IndexFetches: 1})
	w.Flush(&p, d.Add(3*time.Second), true)
	check(3, 7, 3, d)
	var older statistics.Writer
	older.TransactionStopped(a)
	older.IndexAccess(&p, x, enabled, 1, 0, 0)
	older.Flush(&p, d.Add(4*time.Second), true)
	check(4, 7, 3, d)
	if retained.IndexScans != 2 || retained.IndexEntries != 2 || !retained.LastIndexScan.Equal(b) {
		t.Fatal("retained value mutated", retained)
	}
	for _, mode := range []string{"none", "cache", "snapshot"} {
		var r statistics.Reader
		r.Configure(mode, 1)
		before := r.Read(&p, x, a)
		w.IndexAccess(&p, x, enabled, 1, 1, 1)
		w.IndexAccess(&p, y, enabled, 1, 2, 0)
		w.Flush(&p, d, true)
		want := before.IndexScans
		if mode == "none" {
			want++
		}
		if got := r.Read(&p, x, a).IndexScans; got != want {
			t.Fatal(mode, "cached key", got, want)
		}
		if got := r.Read(&p, y, a).IndexScans; mode == "snapshot" && got != p.Read(y).IndexScans-1 {
			t.Fatal(mode, "whole snapshot", got)
		}
		w.IndexAccess(&p, x, enabled, 1, 0, 0)
		published := p.Read(x)
		r.Clear()
		if r.Read(&p, x, a).IndexScans != published.IndexScans || p.Read(x) != published {
			t.Fatal("clear flushed/reset")
		}
		w.Flush(&p, d, true)
	}
	w.IndexAccess(&p, y, enabled, 1, 2, 2)
	p.Retire(y)
	w.Flush(&p, d, true)
	if f := p.Read(y); f.IndexScans != 0 || !f.LastIndexScan.IsZero() {
		t.Fatal("late retirement resurrected", f)
	}
	w.IndexAccess(&p, x, enabled, 1, 2, 2)
	p.Reset(d)
	w.Flush(&p, d, true)
	check(0, 0, 0, time.Time{})
	w.IndexAccess(&p, x, enabled, 1, 2, 2)
	p.Replace(d, nil)
	w.Flush(&p, d, true)
	check(0, 0, 0, time.Time{})
	var fresh statistics.Writer
	fresh.IndexAccess(&p, x, enabled, 1, 0, 0)
	fresh.Flush(&p, d, true)
	check(1, 0, 0, d) // Standalone lazy-stop fallback.
}
