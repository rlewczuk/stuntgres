package statistics_test

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/statistics"
	"sync"
	"testing"
	"time"
)

func statsKeys() (statistics.Key, statistics.Key) {
	d := &ids.Domain{}
	return statistics.Key{Object: d.Ref(1, ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 1})}, statistics.Key{Object: d.Ref(1, ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 2})}
}
func TestStatsProviderPublication(t *testing.T) {
	var p statistics.Provider
	var a, b statistics.Writer
	x, _ := statsKeys()
	now := time.Now() // Includes the monotonic reading used by the real idle seam.
	a.Add(&p, x, statistics.Fact{Inserts: 1})
	if !a.Flush(&p, now, false) {
		t.Fatal("first flush throttled")
	}
	a.Add(&p, x, statistics.Fact{Inserts: 2})
	if a.Flush(&p, now.Add(999*time.Millisecond), false) {
		t.Fatal("early flush")
	}
	var r statistics.Reader
	r.Configure("none", 0)
	r.Clear()
	if got := r.Read(&p, x, now).Inserts; got != 1 {
		t.Fatal("read/clear flushed pending", got)
	}
	b.Add(&p, x, statistics.Fact{Inserts: 4})
	if !b.Flush(&p, now.Add(time.Millisecond), false) {
		t.Fatal("peer borrowed throttle")
	}
	if !a.Flush(&p, now.Add(time.Second), false) || p.Read(x).Inserts != 7 {
		t.Fatal("due batch lost", p.Read(x))
	}
	a.Add(&p, x, statistics.Fact{Inserts: 8})
	if !a.Flush(&p, now.Add(time.Second+time.Millisecond), true) || p.Read(x).Inserts != 15 {
		t.Fatal("terminal drain lost", p.Read(x))
	}
	a.Add(&p, x, statistics.Fact{Live: -3})
	a.Flush(&p, now.Add(2*time.Second), true)
	if p.Read(x).Live != 0 {
		t.Fatal("negative estimate")
	}
	// A wall-clock rollback does not advance or replace the monotonic throttle.
	a.Add(&p, x, statistics.Fact{Inserts: 1})
	if a.Flush(&p, time.Unix(1, 0), false) {
		t.Fatal("wall rollback defeated throttle")
	}
	r.Configure("snapshot", 1)
	r.Read(&p, x, time.Unix(1, 0))
	if !a.Flush(&p, now.Add(3*time.Second), false) || p.Read(x).Inserts != 16 {
		t.Fatal("wall rollback moved monotonic frontier")
	}
}
func TestStatsProviderReaders(t *testing.T) {
	for _, mode := range []string{"none", "cache", "snapshot"} {
		t.Run(mode, func(t *testing.T) {
			var p statistics.Provider
			var w statistics.Writer
			var r statistics.Reader
			x, y := statsKeys()
			now := time.Unix(100, 0)
			w.Add(&p, x, statistics.Fact{Inserts: 1})
			w.Flush(&p, now, true)
			r.Configure(mode, 1)
			if !r.Timestamp().IsZero() {
				t.Fatal("eager timestamp")
			}
			if r.Read(&p, x, now).Inserts != 1 {
				t.Fatal("initial count")
			}
			if (mode == "snapshot") != !r.Timestamp().IsZero() {
				t.Fatal("timestamp mode")
			}
			w.Add(&p, x, statistics.Fact{Inserts: 2})
			w.Add(&p, y, statistics.Fact{Inserts: 2})
			w.Flush(&p, now.Add(time.Second), true)
			wantX, wantY := int64(1), int64(2)
			if mode == "none" {
				wantX = 3
			}
			if mode == "snapshot" {
				wantY = 0
			}
			if r.Read(&p, x, now).Inserts != wantX || r.Read(&p, y, now).Inserts != wantY {
				t.Fatal("object granularity", mode)
			}
			retained := r.Read(&p, x, now)
			w.Add(&p, x, statistics.Fact{Inserts: 5})
			r.Clear()
			if !r.Timestamp().IsZero() || r.Read(&p, x, now).Inserts != 3 || p.Read(x).Inserts != 3 {
				t.Fatal("clear changed published facts")
			}
			if retained.Inserts != wantX {
				t.Fatal("retained value mutated")
			}
			r.Configure("snapshot", 2)
			r.Read(&p, x, now)
			r.Configure("snapshot", 4)
			if !r.Timestamp().IsZero() {
				t.Fatal("effective round trip not invalidated")
			}
		})
	}
	// Missing cache entries are facts too, and an empty snapshot gets a timestamp.
	var p statistics.Provider
	var w statistics.Writer
	var r statistics.Reader
	x, _ := statsKeys()
	now := time.Unix(100, 0)
	r.Configure("cache", 0)
	r.Read(&p, x, now)
	w.Add(&p, x, statistics.Fact{Inserts: 2})
	w.Flush(&p, now, true)
	if r.Read(&p, x, now).Inserts != 0 {
		t.Fatal("missing cache entry not retained")
	}
	var empty statistics.Provider
	r.Configure("snapshot", 1)
	r.Read(&empty, x, now)
	if r.Timestamp() != now {
		t.Fatal("empty capture lacks timestamp")
	}
}
func TestStatsProviderCollection(t *testing.T) {
	t.Run("recursive_usage", statsProviderFunctionUsage)
	var p statistics.Provider
	var w statistics.Writer
	x, _ := statsKeys()
	policy := statistics.Policy{Functions: "none"}
	now := time.Unix(100, 0)
	w.Tuple(&p, x, policy, 7, 2, 1)
	policy.Counts = true
	w.Tuple(&p, x, policy, 3, 1, 2)
	io, wal, fn := x, x, x
	io.Category = statistics.IO
	wal.Category = statistics.WAL
	fn.Category = statistics.Function
	w.IO(&p, io, policy, 3, 7*time.Millisecond)
	policy.IOTiming = true
	w.IO(&p, io, policy, 2, 5*time.Millisecond)
	w.IO(&p, wal, policy, 4, 9*time.Millisecond)
	policy.WALTiming = true
	policy.IOTiming = false
	w.IO(&p, wal, policy, 1, 6*time.Millisecond)
	w.Function(&p, fn, policy, "pl", false, 10*time.Millisecond, 8*time.Millisecond)
	policy.Functions = "pl"
	w.Function(&p, fn, policy, "sql", false, 10*time.Millisecond, 8*time.Millisecond)
	w.Function(&p, fn, policy, "pl", false, 10*time.Millisecond, 8*time.Millisecond)
	policy.Functions = "all"
	w.Function(&p, fn, policy, "sql", true, 30*time.Millisecond, 30*time.Millisecond)
	w.Function(&p, fn, policy, "sql", false, 4*time.Millisecond, 3*time.Millisecond)
	w.Function(&p, fn, policy, "c", false, 2*time.Millisecond, 2*time.Millisecond)
	w.Function(&p, fn, policy, "internal", false, time.Second, time.Second)
	w.Flush(&p, now, true)
	if f := p.Read(x); f.Inserts != 3 || f.Updates != 1 || f.Deletes != 2 {
		t.Fatal("tuple policy", f)
	}
	if f := p.Read(io); f.Operations != 5 || f.TotalTime != 5*time.Millisecond {
		t.Fatal("I/O timing policy", f)
	}
	if f := p.Read(wal); f.Operations != 5 || f.TotalTime != 6*time.Millisecond {
		t.Fatal("WAL timing policy", f)
	}
	if f := p.Read(fn); f.Calls != 3 || f.TotalTime != 16*time.Millisecond || f.SelfTime != 13*time.Millisecond {
		t.Fatal("function matrix", f)
	}
	token := (&ids.Domain{}).Session(1)
	p.Command(token, ids.CommandIdentity{Command: 1}, "SELECT secret", now, true)
	var r statistics.Reader
	if !r.Activities(&p)[token].Active {
		t.Fatal("missing active command")
	}
	p.Command(token, ids.CommandIdentity{Command: 2}, "secret disabled", now, false)
	if !r.Activities(&p)[token].Active {
		t.Fatal("activity snapshot changed")
	}
	r.Clear()
	if a := r.Activities(&p)[token]; !a.Disabled || a.Query != "" || !a.Start.IsZero() || a.Active {
		t.Fatal("disabled activity leaked", a)
	}
	if p.Read(x).Inserts != 3 {
		t.Fatal("activity switch suppressed counts")
	}
	p.End(token)
	r.Clear()
	if len(r.Activities(&p)) != 0 {
		t.Fatal("terminal activity retained")
	}
	var maintenance statistics.Provider
	maintenance.ReportVacuum(x, statistics.Policy{}, now, 7*time.Millisecond, 2, 0)
	if maintenance.Read(x).VacuumCount != 0 {
		t.Fatal("disabled maintenance changed shared collection")
	}
	maintenance.ReportVacuum(x, statistics.Policy{Counts: true}, now, 7*time.Millisecond, 2, 0)
	maintenance.ReportAnalyze(x, statistics.Policy{Counts: true}, now, 5*time.Millisecond, 2, 0, true)
	if f := maintenance.Read(x); f.VacuumCount != 1 || f.AnalyzeCount != 1 || f.VacuumTime != 7*time.Millisecond || f.AnalyzeTime != 5*time.Millisecond || f.LastVacuum != now || f.LastAnalyze != now || f.Live != 2 {
		t.Fatal("shared maintenance count/time", f)
	}
}
func TestStatsProviderResetAndIdentity(t *testing.T) {
	var p statistics.Provider
	var w statistics.Writer
	x, _ := statsKeys()
	next := x
	next.Object.Epoch++
	now := time.Unix(100, 0)
	w.Add(&p, x, statistics.Fact{Inserts: 1})
	w.Flush(&p, now, true)
	if p.Read(next).Inserts != 0 {
		t.Fatal("epoch identity reused")
	}
	foreign := x
	foreign.Object = (&ids.Domain{}).Ref(x.Object.Epoch, x.Object.Address)
	if p.Read(foreign).Inserts != 0 {
		t.Fatal("database identity reused")
	}
	w.Add(&p, x, statistics.Fact{Inserts: 2})
	p.Reset(now.Add(time.Second))
	w.Flush(&p, now.Add(time.Second), true)
	if f := p.Read(x); f.Inserts != 0 || f.Generation != 1 || f.ResetAt != now.Add(time.Second) {
		t.Fatal("reset mixed pending epochs", f)
	}
	baseline := map[statistics.Key]statistics.Fact{next: {Live: 2}}
	p.Replace(now.Add(2*time.Second), baseline)
	if f := p.Read(next); f.Live != 2 || f.Inserts != 0 || f.Dead != 0 || f.VacuumCount != 0 || !f.LastVacuum.IsZero() || f.Generation != 2 {
		t.Fatal("replacement invented collection history", f)
	}
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() {
			var writer statistics.Writer
			for range 100 {
				writer.Add(&p, x, statistics.Fact{Inserts: 1})
				writer.Flush(&p, now, true)
				var reader statistics.Reader
				reader.Configure("snapshot", 0)
				reader.Read(&p, x, now)
				reader.Clear()
			}
		})
	}
	workers.Wait()
	if p.Read(x).Inserts != 800 {
		t.Fatal("lost concurrent increments", p.Read(x))
	}
	// Concurrent reset/read/publication must keep timestamp and reset epoch coherent.
	workers.Go(func() {
		for range 100 {
			p.Reset(now)
		}
	})
	workers.Go(func() {
		var writer statistics.Writer
		for range 100 {
			writer.Add(&p, x, statistics.Fact{Inserts: 1})
			writer.Flush(&p, now, true)
			var reader statistics.Reader
			reader.Configure("snapshot", 0)
			f := reader.Read(&p, x, now)
			if reader.Timestamp() != now || f.Generation == 0 {
				t.Error("mixed capture")
			}
		}
	})
	workers.Wait()
}
