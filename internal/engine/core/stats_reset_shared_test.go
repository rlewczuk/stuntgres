package core_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/statistics"
	"testing"
	"time"
)

func sharedStatsFixture(t *testing.T) (*core.Engine, *session.Session, *core.Engine, *session.Session, statistics.Key, statistics.Key) {
	t.Helper()
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	statsFunctionExec(t, s, "CREATE TABLE shared_stats_control(v integer)", "CREATE DATABASE shared_stats_peer")
	peer, err := e.SelectDatabase(context.Background(), "shared_stats_peer")
	if err != nil {
		t.Fatal(err)
	}
	ps := enginetest.StartSession(t, peer, nil)
	statsFunctionExec(t, ps, "CREATE TABLE shared_stats_peer_control(v integer)")
	return e, s, peer, ps, statistics.Key{Object: enginetest.RoleTableRef(t, e, "shared_stats_control"), Category: statistics.IO}, statistics.Key{Object: enginetest.RoleTableRef(t, peer, "shared_stats_peer_control"), Category: statistics.IO}
}
func sharedStatsSeed(e *core.Engine, key statistics.Key) {
	var w statistics.Writer
	at := time.Unix(100, 0).UTC()
	e.Statistics.Reset(at)
	for k := statistics.SharedKind(0); k < statistics.SharedKindCount; k++ {
		w.Shared(&e.Statistics, k, statistics.SharedFact{Counters: [8]int64{int64(k+1) * 11}, TotalTime: time.Millisecond})
	}
	for k := statistics.SLRUKind(0); k < statistics.SLRUCount; k++ {
		w.SLRU(&e.Statistics, k, statistics.SLRUFact{Counters: [7]int64{int64(k) + 1}})
	}
	w.Add(&e.Statistics, key, statistics.Fact{Operations: 10, TotalTime: 20 * time.Millisecond})
	wal, relation := key, key
	wal.Category, relation.Category = statistics.WAL, statistics.Relation
	w.Add(&e.Statistics, wal, statistics.Fact{Operations: 20, TotalTime: 30 * time.Millisecond})
	w.Add(&e.Statistics, relation, statistics.Fact{Inserts: 9})
	w.Flush(&e.Statistics, at, true)
}
func sharedStatsBackend(e *core.Engine, s *session.Session, io, wal int64) {
	var w statistics.Writer
	w.Backend(&e.Statistics, s.Token(), statistics.Fact{Operations: io}, statistics.Fact{Operations: wal})
	w.Flush(&e.Statistics, time.Unix(100, 0), true)
}
func TestStatsResetSharedRuntime(t *testing.T) {
	e, s, peer, _, key, pk := sharedStatsFixture(t)
	for target := statistics.SharedKind(0); target < statistics.SharedKindCount; target++ {
		t.Run(target.Name(), func(t *testing.T) {
			sharedStatsSeed(e, key)
			sharedStatsSeed(peer, pk)
			var before [statistics.SharedKindCount]statistics.SharedFact
			for k := range before {
				before[k] = e.Statistics.ReadShared(statistics.SharedKind(k))
			}
			query := "SELECT pg_stat_reset_shared($1::text)"
			ast, d := enginetest.PreparedDescription(t, e, s, query)
			for k, want := range before {
				if e.Statistics.ReadShared(statistics.SharedKind(k)) != want {
					t.Fatal("Describe reset")
				}
			}
			argument := target.Name()
			if err := resetStatsPrepared(e, s, ast, &d, &argument); err != nil {
				t.Fatal(err)
			}
			for k, want := range before {
				got := e.Statistics.ReadShared(statistics.SharedKind(k))
				if statistics.SharedKind(k) == target {
					if got.Counters != ([8]int64{}) || got.TotalTime != 0 || got.ResetAt.IsZero() {
						t.Fatal("selected", target, got)
					}
				} else if got != want {
					t.Fatal("unselected", k, got, want)
				}
			}
			for _, entry := range []struct {
				e *core.Engine
				k statistics.Key
			}{{e, key}, {peer, pk}} {
				io := entry.e.Statistics.Read(entry.k)
				walKey, relation := entry.k, entry.k
				walKey.Category, relation.Category = statistics.WAL, statistics.Relation
				wal := entry.e.Statistics.Read(walKey)
				wantIO, wantWAL := int64(10), int64(20)
				if target == statistics.SharedIO {
					wantIO = 0
				}
				if target == statistics.SharedWAL {
					wantWAL = 0
				}
				if io.Operations != wantIO || wal.Operations != wantWAL || entry.e.Statistics.Read(relation).Inserts < 9 {
					t.Fatal("cross database/keyed isolation", io, wal)
				}
			}
			resetStatsExec(t, s, "SELECT pg_stat_reset_shared('"+target.Name()+"')")
		})
	}
	for _, q := range []string{"SELECT pg_stat_reset_shared()", "SELECT pg_stat_reset_shared(NULL)", "SELECT pg_stat_reset_shared(target => NULL)"} {
		sharedStatsSeed(e, key)
		sharedStatsSeed(peer, pk)
		resetStatsExec(t, s, q)
		for _, target := range []*core.Engine{e, peer} {
			for k := statistics.SharedKind(0); k < statistics.SharedKindCount; k++ {
				if f := target.Statistics.ReadShared(k); f.Counters != ([8]int64{}) || f.ResetAt.IsZero() {
					t.Fatal("all", k, f)
				}
			}
		}
	}
	sharedStatsSeed(e, key)
	before := e.Statistics.Read(key)
	if err := resetStatsSQL(t, s, "SELECT pg_stat_reset_shared('WAL')"); err == nil || e.Statistics.Read(key) != before {
		t.Fatal("invalid target mutated state", err)
	}
	for _, name := range []string{"unknown", "", "TRANSACTION", "transaction "} {
		sharedStatsSeed(e, key)
		tx := e.Statistics.ReadSLRU(statistics.Transaction)
		resetStatsExec(t, s, "SELECT pg_stat_reset_slru('"+name+"')")
		if e.Statistics.ReadSLRU(statistics.OtherSLRU).Counters != ([7]int64{}) || e.Statistics.ReadSLRU(statistics.Transaction) != tx {
			t.Fatal("unknown SLRU did not isolate other")
		}
	}
	for target := statistics.SLRUKind(0); target < statistics.SLRUCount; target++ {
		sharedStatsSeed(e, key)
		sharedStatsSeed(peer, pk)
		resetStatsExec(t, s, "SELECT pg_stat_reset_slru(target => '"+target.Name()+"')")
		for _, p := range []*statistics.Provider{&e.Statistics, &peer.Statistics} {
			for k := statistics.SLRUKind(0); k < statistics.SLRUCount; k++ {
				want := int64(k) + 1
				if k == target {
					want = 0
				}
				if f := p.ReadSLRU(k); f.Counters[0] != want || k == target && f.ResetAt.IsZero() {
					t.Fatal("named SLRU isolation", target, k, f)
				}
			}
		}
	}
	for _, q := range []string{"SELECT pg_stat_reset_slru()", "SELECT pg_stat_reset_slru(NULL)"} {
		resetStatsExec(t, s, q)
		for k := statistics.SLRUKind(0); k < statistics.SLRUCount; k++ {
			if e.Statistics.ReadSLRU(k).Counters != ([7]int64{}) || peer.Statistics.ReadSLRU(k).Counters != ([7]int64{}) {
				t.Fatal("SLRU all", k)
			}
		}
	}
	sharedStatsSeed(e, key)
	unchanged := e.Statistics.Read(key)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := enginetest.Execute(e, canceled, enginetest.Statement(t, "SELECT pg_stat_reset_shared('io')"), nil); !errors.Is(err, context.Canceled) {
		t.Fatal("pre-cancel classification", err)
	}
	expired, expire := context.WithDeadline(context.Background(), time.Unix(1, 0))
	defer expire()
	if _, err := enginetest.Execute(e, expired, enginetest.Statement(t, "SELECT pg_stat_reset_shared('io')"), nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("expired classification", err)
	}
	if e.Statistics.Read(key) != unchanged {
		t.Fatal("canceled reset changed published state")
	}
}
func TestStatsResetSharedBackendLifetime(t *testing.T) {
	e, s, peer, ps, key, pk := sharedStatsFixture(t)
	sharedStatsSeed(e, key)
	sharedStatsSeed(peer, pk)
	sharedStatsBackend(e, s, 11, 13)
	sharedStatsBackend(peer, ps, 17, 19)
	sharedStatsBackend(e, ps, 23, 29)
	a, _ := e.Statistics.ReadBackend(s.Token())
	beforeIO := e.Statistics.Read(key)
	statsFunctionExec(t, s, "SELECT pg_stat_reset_backend_stats(NULL) IS NULL")
	if got, _ := e.Statistics.ReadBackend(s.Token()); got != a {
		t.Fatal("strict NULL reset backend")
	}
	// A caller in database A resolves the actual PID registered in database B,
	// and clears that token's retained partitions in both databases.
	var activity statistics.Reader
	activityBefore := activity.Activities(&e.Statistics)[ps.Token()]
	resetStatsExec(t, s, fmt.Sprintf("SELECT pg_stat_reset_backend_stats(%d)", ps.Token().ID()))
	for _, p := range []*statistics.Provider{&e.Statistics, &peer.Statistics} {
		f, ok := p.ReadBackend(ps.Token())
		if !ok || f.IO.Operations != 0 || f.WAL.Operations != 0 || f.ResetAt.IsZero() {
			t.Fatal("real backend PID", f, ok)
		}
	}
	if got, _ := e.Statistics.ReadBackend(s.Token()); got != a || e.Statistics.Read(key) != beforeIO {
		t.Fatal("backend reset changed peer/shared")
	}
	var currentActivity statistics.Reader
	if got := currentActivity.Activities(&e.Statistics)[ps.Token()]; got != activityBefore {
		t.Fatal("backend reset changed target activity", got, activityBefore)
	}
	for _, pid := range []int64{0, -1, 2147483647} {
		resetStatsExec(t, s, fmt.Sprintf("SELECT pg_stat_reset_backend_stats(%d)", pid))
	}
	token := ps.Token()
	var late statistics.Writer
	late.Backend(&e.Statistics, token, statistics.Fact{Operations: 9}, statistics.Fact{Operations: 8})
	ps.RequestEnd(session.CleanClose)
	if err := ps.WaitEnd(context.Background()); err != nil {
		t.Fatal(err)
	}
	resetStatsExec(t, s, fmt.Sprintf("SELECT pg_stat_reset_backend_stats(%d)", token.ID()))
	late.Flush(&e.Statistics, time.Now(), true)
	late.Backend(&e.Statistics, token, statistics.Fact{Operations: 7}, statistics.Fact{})
	late.Flush(&e.Statistics, time.Now(), true)
	for _, p := range []*statistics.Provider{&e.Statistics, &peer.Statistics} {
		if _, ok := p.ReadBackend(token); ok {
			t.Fatal("detached token resurrected")
		}
	}
	if got, _ := e.Statistics.ReadBackend(s.Token()); got != a {
		t.Fatal("end changed independent lifetime")
	}
	t.Run("bounded_actual_session_lifetimes", sharedStatsBackendRegistrationChurn)
	t.Run("new_target_positive_authority", sharedStatsBackendTargetInheritance)
}
func TestStatsResetSharedClusterIsolation(t *testing.T) {
	e, s, peer, ps, key, pk := sharedStatsFixture(t)
	sharedStatsSeed(e, key)
	sharedStatsSeed(peer, pk)
	sharedStatsBackend(peer, ps, 17, 19)
	sharedStatsBackend(e, s, 11, 13)
	var dump bytes.Buffer
	if err := engine.Dump(context.Background(), e, &dump); err != nil {
		t.Fatal(err)
	}
	independent := engine.New()
	is := enginetest.StartSession(t, independent, nil)
	statsFunctionExec(t, is, "CREATE TABLE independent_shared(v integer)")
	ik := statistics.Key{Object: enginetest.RoleTableRef(t, independent, "independent_shared"), Category: statistics.IO}
	sharedStatsSeed(independent, ik)
	before := independent.Statistics.Read(ik)
	backend, _ := peer.Statistics.ReadBackend(ps.Token())
	wal := pk
	wal.Category = statistics.WAL
	peerWAL := peer.Statistics.Read(wal)
	resetStatsExec(t, s, "SELECT pg_stat_reset_shared('io')")
	if e.Statistics.Read(key).Operations != 0 || peer.Statistics.Read(pk).Operations != 0 || peer.Statistics.Read(wal) != peerWAL {
		t.Fatal("cluster selected traversal")
	}
	resetStatsExec(t, s, "SELECT pg_stat_reset_shared(NULL)")
	if got, _ := peer.Statistics.ReadBackend(ps.Token()); got != backend || independent.Statistics.Read(ik) != before {
		t.Fatal("all crossed backend/driver boundary")
	}
	if err := engine.Reset(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.Statistics.ReadBackend(s.Token()); ok {
		t.Fatal("native replacement retained backend history")
	}
	if got, _ := peer.Statistics.ReadBackend(ps.Token()); got != backend {
		t.Fatal("native replacement changed peer database")
	}
	sharedStatsBackend(e, s, 3, 4)
	if err := engine.Restore(context.Background(), e, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.Statistics.ReadBackend(s.Token()); ok {
		t.Fatal("Restore serialized backend monitoring history")
	}
	if got, _ := peer.Statistics.ReadBackend(ps.Token()); got != backend {
		t.Fatal("Restore changed peer backend")
	}
	var late statistics.Writer
	sharedStatsBackend(e, s, 3, 4)
	late.Backend(&e.Statistics, s.Token(), statistics.Fact{Operations: 9}, statistics.Fact{})
	if err := engine.Reset(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	late.Flush(&e.Statistics, time.Now(), true)
	if _, ok := e.Statistics.ReadBackend(s.Token()); ok {
		t.Fatal("old generation recreated backend")
	}
}
func TestStatsResetSharedProviderSmoke(t *testing.T) {
	e, s, peer, ps, key, pk := sharedStatsFixture(t)
	sharedStatsSeed(e, key)
	sharedStatsSeed(peer, pk)
	sharedStatsBackend(e, s, 11, 13)
	sharedStatsBackend(peer, ps, 17, 19)
	var slruSeed statistics.Writer
	slruSeed.SLRU(&e.Statistics, statistics.OtherSLRU, statistics.SLRUFact{Counters: [7]int64{1}})
	slruSeed.Flush(&e.Statistics, time.Unix(100, 0), true)
	if e.Statistics.ReadSLRU(statistics.Transaction).Counters[0] != 7 || e.Statistics.ReadSLRU(statistics.OtherSLRU).Counters[0] != 9 {
		t.Fatal("positive SLRU smoke seed")
	}
	var cache statistics.Reader
	cache.Configure("snapshot", 1)
	cache.Read(&e.Statistics, key, time.Unix(100, 0))
	stamp := cache.Timestamp()
	before := e.Statistics.Read(key)
	var pending statistics.Writer
	pending.Add(&e.Statistics, key, statistics.Fact{Operations: 3, TotalTime: 4 * time.Millisecond})
	ast, d := enginetest.PreparedDescription(t, e, s, "SELECT pg_stat_reset_shared($1::text)")
	arg := "io"
	if err := resetStatsPrepared(e, s, ast, &d, &arg); err != nil {
		t.Fatal(err)
	}
	if e.Statistics.Read(key).Operations != 0 || peer.Statistics.Read(pk).Operations != 0 || cache.Read(&e.Statistics, key, time.Now()) != before || cache.Timestamp() != stamp {
		t.Fatal("prepared cross database/reset/cache")
	}
	transaction := e.Statistics.ReadSLRU(statistics.Transaction)
	resetStatsExec(t, s, "SELECT pg_stat_reset_slru('unknown')")
	if e.Statistics.ReadSLRU(statistics.OtherSLRU).Counters[0] != 0 || e.Statistics.ReadSLRU(statistics.Transaction) != transaction {
		t.Fatal("SLRU other")
	}
	b, _ := peer.Statistics.ReadBackend(ps.Token())
	resetStatsExec(t, s, fmt.Sprintf("SELECT pg_stat_reset_backend_stats(%d)", s.Token().ID()))
	a, _ := e.Statistics.ReadBackend(s.Token())
	if a.IO.Operations != 0 || a.WAL.Operations != 0 {
		t.Fatal("backend A")
	}
	if got, _ := peer.Statistics.ReadBackend(ps.Token()); got != b {
		t.Fatal("backend B")
	}
	wal := key
	wal.Category = statistics.WAL
	if e.Statistics.Read(wal).Operations != 20 {
		t.Fatal("shared WAL")
	}
	pending.Flush(&e.Statistics, time.Unix(101, 0), false)
	if got := e.Statistics.Read(key); got.Operations != 3 || got.TotalTime != 4*time.Millisecond || got.Generation != before.Generation {
		t.Fatal("pending prefix", got)
	}
	e.Statistics.ReportPrefetchGauges(3, 5, 7)
	resetStatsExec(t, s, "SELECT pg_stat_reset_shared('recovery_prefetch')")
	f := e.Statistics.ReadShared(statistics.RecoveryPrefetch)
	if f.Counters != ([8]int64{}) || f.WALDistance != 3 || f.BlockDistance != 5 || f.IODepth != 7 {
		t.Fatal("prefetch", f)
	}
	var w statistics.Writer
	w.Shared(&e.Statistics, statistics.Archiver, statistics.SharedFact{LastSuccess: "wal", LastSuccessAt: time.Unix(100, 0), Counters: [8]int64{1}})
	w.Flush(&e.Statistics, time.Now(), true)
	resetStatsExec(t, s, "SELECT pg_stat_reset_shared('archiver')")
	arch := e.Statistics.ReadShared(statistics.Archiver)
	if arch != (statistics.SharedFact{ResetAt: arch.ResetAt}) || arch.ResetAt.IsZero() {
		t.Fatal("archiver", arch)
	}
	if reflect.ValueOf(&pending).Elem().FieldByName("last").IsZero() {
		t.Fatal("ordinary publication frontier absent")
	}
}
