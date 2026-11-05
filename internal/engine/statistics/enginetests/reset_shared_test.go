package statistics_test

import (
	"reflect"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/statistics"
	"testing"
	"time"
)

func sharedVector(n int64) statistics.SharedFact {
	return statistics.SharedFact{Counters: [8]int64{n, n + 1, n + 2, n + 3, n + 4, n + 5}, TotalTime: time.Duration(n) * time.Millisecond}
}
func slruVector(n int64) statistics.SLRUFact {
	return statistics.SLRUFact{Counters: [7]int64{n, n + 1, n + 2, n + 3, n + 4, n + 5, n + 6}}
}
func sharedSeed(p *statistics.Provider, k statistics.SharedKind, n int64, at time.Time) {
	var w statistics.Writer
	w.Shared(p, k, sharedVector(n))
	w.Flush(p, at, true)
}
func TestStatsProviderResetShared(t *testing.T) {
	t0, t1 := time.Unix(100, 0).UTC(), time.Unix(200, 0).UTC()
	for target := statistics.SharedKind(0); target < statistics.SharedKindCount; target++ {
		t.Run(target.Name(), func(t *testing.T) {
			var p statistics.Provider
			p.Reset(t0)
			a, b := statsKeys()
			a.Category, b.Category = statistics.IO, statistics.WAL
			relation, function, slot, subscription := a, a, a, a
			relation.Category, function.Category, slot.Category, subscription.Category = statistics.Relation, statistics.Function, statistics.ReplicationSlot, statistics.Subscription
			var w statistics.Writer
			w.Add(&p, a, statistics.Fact{Operations: 44, TotalTime: 44 * time.Millisecond})
			w.Add(&p, b, statistics.Fact{Operations: 77, TotalTime: 77 * time.Millisecond})
			controls := map[statistics.Key]statistics.Fact{relation: {Inserts: 9}, function: {Calls: 8}, slot: {SpillTxns: 7}, subscription: {ApplyErrorCount: 6}}
			for key, f := range controls {
				w.Add(&p, key, f)
			}
			w.Flush(&p, t0, true)
			for key := range controls {
				controls[key] = p.Read(key)
			}
			for k := statistics.SharedKind(0); k < statistics.SharedKindCount; k++ {
				sharedSeed(&p, k, int64(k+1)*11, t0)
			}
			w.SLRU(&p, statistics.Transaction, slruVector(66))
			w.Flush(&p, t0, true)
			token := (&ids.Domain{}).Session(1)
			p.RegisterActivity(token, statistics.Activity{Principal: statsLogin()})
			p.Command(token, ids.CommandIdentity{}, "activity control", t0, true)
			var ar statistics.Reader
			beforeActivity := ar.Activities(&p)
			var before [statistics.SharedKindCount]statistics.SharedFact
			for k := range before {
				before[k] = p.ReadShared(statistics.SharedKind(k))
			}
			beforeIO, beforeWAL := p.Read(a), p.Read(b)
			p.ResetShared(target, t1)
			for k := range before {
				got := p.ReadShared(statistics.SharedKind(k))
				want := before[k]
				if statistics.SharedKind(k) == target {
					want = statistics.SharedFact{ResetAt: t1}
				}
				if got != want {
					t.Fatal("family isolation", k, got, want)
				}
			}
			for _, key := range []statistics.Key{a, b} {
				want := beforeIO
				selected := target == statistics.SharedIO
				if key == b {
					want = beforeWAL
					selected = target == statistics.SharedWAL
				}
				if selected {
					want.Operations, want.TotalTime, want.ResetAt = 0, 0, t1
				}
				if got := p.Read(key); got != want {
					t.Fatal("keyed reset", key, got, want)
				}
			}
			for key, want := range controls {
				if p.Read(key) != want {
					t.Fatal("unrelated category", key)
				}
			}
			var afterActivity statistics.Reader
			if !reflect.DeepEqual(beforeActivity, afterActivity.Activities(&p)) {
				t.Fatal("activity reset")
			}
			p.ResetAllShared(t1)
			for k := statistics.SharedKind(0); k < statistics.SharedKindCount; k++ {
				if got := p.ReadShared(k); got != (statistics.SharedFact{ResetAt: t1}) {
					t.Fatal(k, got)
				}
			}
			for key, want := range controls {
				if p.Read(key) != want {
					t.Fatal("all changed control", key)
				}
			}
		})
	}
	t.Run("concurrent_atomic_capture", func(t *testing.T) {
		var p statistics.Provider
		start, done := make(chan struct{}), make(chan struct{})
		go func() {
			defer close(done)
			<-start
			var w statistics.Writer
			for range 100 {
				w.Shared(&p, statistics.Checkpointer, sharedVector(11))
				w.Flush(&p, t0, true)
				p.ResetShared(statistics.Checkpointer, t1)
			}
		}()
		close(start)
		var invalid statistics.SharedFact
		for range 100 {
			f := p.ReadShared(statistics.Checkpointer)
			zero := f.Counters == ([8]int64{}) && f.TotalTime == 0
			positive := f.Counters == sharedVector(11).Counters && f.TotalTime == 11*time.Millisecond
			if !zero && !positive {
				invalid = f
			}
		}
		<-done
		if invalid != (statistics.SharedFact{}) {
			t.Fatal("partial fixed vector", invalid)
		}
	})
}
func TestStatsProviderResetSLRU(t *testing.T) {
	var p statistics.Provider
	var w statistics.Writer
	at := time.Unix(100, 0).UTC()
	for k := statistics.SLRUKind(0); k < statistics.SLRUCount; k++ {
		w.SLRU(&p, k, slruVector(int64(k)+1))
	}
	w.Flush(&p, at, true)
	var cache statistics.Reader
	cache.Configure("cache", 1)
	cache.ReadSLRU(&p, statistics.Transaction, at)
	p.ResetSLRU(statistics.SLRUTarget("transaction"), at)
	for k := statistics.SLRUKind(0); k < statistics.SLRUCount; k++ {
		want := slruVector(int64(k) + 1)
		if k == statistics.Transaction {
			want = statistics.SLRUFact{ResetAt: at}
		}
		if got := p.ReadSLRU(k); got != want {
			t.Fatal(k, got, want)
		}
	}
	for _, name := range []string{"unknown", "", "TRANSACTION", "transaction ", "other"} {
		if statistics.SLRUTarget(name) != statistics.OtherSLRU {
			t.Fatal("unknown target", name)
		}
		p.ResetSLRU(statistics.SLRUTarget(name), at)
	}
	if cache.ReadSLRU(&p, statistics.OtherSLRU, at) != slruVector(8) {
		t.Fatal("SLRU cache was per entry, not per kind")
	}
	cache.Clear()
	if cache.ReadSLRU(&p, statistics.OtherSLRU, at) != (statistics.SLRUFact{ResetAt: at}) {
		t.Fatal("clear")
	}
	p.ResetShared(statistics.SLRU, at)
	for k := statistics.SLRUKind(0); k < statistics.SLRUCount; k++ {
		if p.ReadSLRU(k) != (statistics.SLRUFact{ResetAt: at}) {
			t.Fatal("all SLRUs", k)
		}
	}
	if p.ReadShared(statistics.Archiver) != (statistics.SharedFact{}) {
		t.Fatal("SLRU changed archiver")
	}
}
func TestStatsProviderResetBackend(t *testing.T) {
	var p statistics.Provider
	d := new(ids.Domain)
	a, b := d.Session(1), d.Session(2)
	var w statistics.Writer
	at := time.Unix(100, 0).UTC()
	p.BeginBackend(a, 1)
	p.BeginBackend(b, 2)
	io, wal := statsKeys()
	io.Category, wal.Category = statistics.IO, statistics.WAL
	w.Add(&p, io, statistics.Fact{Operations: 23})
	w.Add(&p, wal, statistics.Fact{Operations: 29})
	w.Backend(&p, a, statistics.Fact{Operations: 11}, statistics.Fact{Operations: 13})
	w.Backend(&p, b, statistics.Fact{Operations: 17}, statistics.Fact{Operations: 19})
	w.Flush(&p, at, true)
	var retained statistics.Reader
	retained.Configure("snapshot", 1)
	originalA, _ := retained.ReadBackend(&p, statistics.BackendIdentity{Token: a, Incarnation: 1}, at)
	beforeB, _ := p.ReadBackend(b)
	if !p.ResetBackend(a, at) {
		t.Fatal("missing A")
	}
	got, ok := p.ReadBackend(a)
	if !ok || got != (statistics.BackendFact{IO: statistics.Fact{ResetAt: at}, WAL: statistics.Fact{ResetAt: at}, ResetAt: at}) {
		t.Fatal(got, ok)
	}
	if after, _ := p.ReadBackend(b); after != beforeB {
		t.Fatal("peer changed")
	}
	if p.Read(io).Operations != 23 || p.Read(wal).Operations != 29 {
		t.Fatal("backend reset changed shared IO/WAL")
	}
	if p.ResetBackend(d.Session(99), at) || reflect.ValueOf(&p).Elem().FieldByName("backends").Len() != 2 {
		t.Fatal("missing allocated")
	}
	w.Backend(&p, a, statistics.Fact{Operations: 3}, statistics.Fact{Operations: 4})
	p.End(a)
	w.Flush(&p, at, true)
	w.Backend(&p, a, statistics.Fact{Operations: 9}, statistics.Fact{})
	w.Flush(&p, at, true)
	if _, ok := p.ReadBackend(a); ok {
		t.Fatal("ended token resurrected")
	}
	if after, _ := p.ReadBackend(b); after != beforeB {
		t.Fatal("end changed peer")
	}
	if cached, ok := retained.ReadBackend(&p, statistics.BackendIdentity{Token: a, Incarnation: 1}, at); !ok || cached != originalA {
		t.Fatal("end changed retained backend snapshot", cached, ok)
	}
	p.Replace(at, nil)
	w.Backend(&p, a, statistics.Fact{Operations: 9}, statistics.Fact{})
	w.Flush(&p, at, true)
	if _, ok := p.ReadBackend(a); ok {
		t.Fatal("replacement lost lifetime fence")
	}
	t.Run("no_nonparticipant_history", statsProviderBackendNoParticipants)
	t.Run("bounded_public_lifetimes", statsProviderBackendRegistrationChurn)
	t.Run("positive_target_inheritance", statsProviderBackendRegistrationInheritance)
}
func TestStatsProviderResetSharedPending(t *testing.T) {
	var p statistics.Provider
	at := time.Unix(100, 0).UTC()
	a, b := statsKeys()
	a.Category, b.Category = statistics.IO, statistics.WAL
	token := new(ids.Domain).Session(1)
	p.BeginBackend(token, 1)
	var w statistics.Writer
	for _, key := range []statistics.Key{a, b} {
		w.Add(&p, key, statistics.Fact{Operations: 10, TotalTime: 20 * time.Millisecond})
	}
	w.Shared(&p, statistics.Archiver, sharedVector(10))
	w.SLRU(&p, statistics.Transaction, slruVector(10))
	w.Backend(&p, token, statistics.Fact{Operations: 10}, statistics.Fact{Operations: 20})
	w.Flush(&p, at, false)
	for _, key := range []statistics.Key{a, b} {
		w.Add(&p, key, statistics.Fact{Operations: 3, TotalTime: 4 * time.Millisecond})
	}
	w.Shared(&p, statistics.Archiver, sharedVector(3))
	w.SLRU(&p, statistics.Transaction, slruVector(3))
	w.Backend(&p, token, statistics.Fact{Operations: 3}, statistics.Fact{Operations: 4})
	before := p.Read(a)
	p.ResetAllShared(at)
	p.ResetBackend(token, at)
	if p.Read(a).Operations != 0 || p.Read(a).TotalTime != 0 || p.Read(a).Generation != before.Generation || !w.Pending() || w.Flush(&p, at.Add(time.Millisecond), false) {
		t.Fatal("reset changed pending/throttle/generation")
	}
	if !w.Flush(&p, at.Add(time.Second), false) {
		t.Fatal("ordinary publication withheld")
	}
	for _, key := range []statistics.Key{a, b} {
		got := p.Read(key)
		if got.Operations != 3 || got.TotalTime != 4*time.Millisecond || got.ResetAt != at {
			t.Fatal("pending prefix", got)
		}
	}
	want := sharedVector(3)
	want.ResetAt = at
	slruWant := slruVector(3)
	slruWant.ResetAt = at
	if p.ReadShared(statistics.Archiver) != want || p.ReadSLRU(statistics.Transaction) != slruWant {
		t.Fatal("fixed pending")
	}
	backend, _ := p.ReadBackend(token)
	if backend.IO.Operations != 3 || backend.WAL.Operations != 4 || backend.ResetAt != at {
		t.Fatal("backend pending", backend)
	}
	w.Add(&p, a, statistics.Fact{Operations: 9})
	p.Retire(a)
	p.ResetAllShared(at)
	w.Flush(&p, at.Add(2*time.Second), false)
	if p.Read(a).Operations != 0 {
		t.Fatal("reset removed retirement fence")
	}
}
func TestStatsProviderResetSharedReaders(t *testing.T) {
	at, next := time.Unix(100, 0).UTC(), time.Unix(200, 0).UTC()
	token := new(ids.Domain).Session(1)
	for _, mode := range []string{"none", "cache", "snapshot"} {
		t.Run(mode, func(t *testing.T) {
			var p statistics.Provider
			p.Reset(at)
			p.BeginBackend(token, 1)
			sharedSeed(&p, statistics.Archiver, 10, at)
			a, _ := statsKeys()
			var w statistics.Writer
			w.Add(&p, a, statistics.Fact{Inserts: 9})
			w.Backend(&p, token, statistics.Fact{Operations: 11}, statistics.Fact{Operations: 13})
			w.Flush(&p, at, true)
			var r statistics.Reader
			r.Configure(mode, 1)
			before := r.ReadShared(&p, statistics.Archiver, at)
			backend, _ := r.ReadBackend(&p, statistics.BackendIdentity{Token: token, Incarnation: 1}, at)
			p.ResetAllShared(next)
			p.ResetBackend(token, next)
			got := r.ReadShared(&p, statistics.Archiver, next)
			after, _ := r.ReadBackend(&p, statistics.BackendIdentity{Token: token, Incarnation: 1}, next)
			if mode == "none" {
				if got.Counters[0] != 0 || after.IO.Operations != 0 {
					t.Fatal("none stale")
				}
			} else if got != before || after != backend {
				t.Fatal("retained reader changed")
			}
			if mode == "snapshot" && r.Timestamp() != at {
				t.Fatal("snapshot timestamp")
			}
			r.Clear()
			if r.ReadShared(&p, statistics.Archiver, next).Counters[0] != 0 {
				t.Fatal("clear")
			}
			var firstRelation statistics.Reader
			firstRelation.Configure("snapshot", 1)
			sharedSeed(&p, statistics.BGWriter, 22, at)
			firstRelation.Read(&p, a, at)
			p.ResetShared(statistics.BGWriter, next)
			if firstRelation.ReadShared(&p, statistics.BGWriter, next).Counters[0] != 22 {
				t.Fatal("keyed first capture missed fixed state")
			}
			var firstFixed statistics.Reader
			firstFixed.Configure("snapshot", 1)
			firstFixed.ReadShared(&p, statistics.Checkpointer, at)
			p.Reset(next)
			if firstFixed.Read(&p, a, next).Inserts != 9 {
				t.Fatal("fixed first capture missed keyed state")
			}
		})
	}
	var p statistics.Provider
	var w statistics.Writer
	arch := sharedVector(10)
	arch.LastSuccess, arch.LastFailure = "success", "failure"
	arch.LastSuccessAt, arch.LastFailureAt = at, at
	w.Shared(&p, statistics.Archiver, arch)
	w.Shared(&p, statistics.RecoveryPrefetch, sharedVector(55))
	w.Flush(&p, at, true)
	p.ReportPrefetchGauges(3, 5, 7)
	for _, mode := range []string{"none", "cache", "snapshot"} {
		var r statistics.Reader
		r.Configure(mode, 1)
		w.Shared(&p, statistics.RecoveryPrefetch, sharedVector(55))
		w.Flush(&p, at, true)
		if r.ReadShared(&p, statistics.RecoveryPrefetch, at).Counters[0] == 0 || !r.Timestamp().IsZero() {
			t.Fatal("prefetch constructed snapshot or lost positive event", mode)
		}
		p.ResetAllShared(next)
		f := r.ReadShared(&p, statistics.RecoveryPrefetch, next)
		if f != (statistics.SharedFact{ResetAt: next, WALDistance: 3, BlockDistance: 5, IODepth: 7}) || !r.Timestamp().IsZero() {
			t.Fatal("prefetch reset/cache", mode, f)
		}
	}
	if p.ReadShared(statistics.Archiver) != (statistics.SharedFact{ResetAt: next}) {
		t.Fatal("archiver history retained")
	}
	var retained statistics.Reader
	retained.Configure("snapshot", 1)
	relation, missing := statsKeys()
	missing.Category = statistics.IO
	retained.Read(&p, relation, at)
	oldReset := p.Read(missing).ResetAt
	p.ResetShared(statistics.SharedIO, next.Add(time.Second))
	if got := retained.Read(&p, missing, next); got.ResetAt != oldReset || got.Operations != 0 {
		t.Fatal("snapshot absent IO default changed", got)
	}
}
