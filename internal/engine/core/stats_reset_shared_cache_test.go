package core_test

import (
	"context"
	"reflect"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/statistics"
	"testing"
)

// The scoped shared/backend kinds have no SQL getters. Trigger the physical
// reader through its existing relation consumer and inspect owned all-kind
// backing, rather than introducing an inspection export or neighboring getter.
func sharedStatsCapture(t *testing.T, e *core.Engine, s *session.Session, ref ids.ObjectRef) {
	t.Helper()
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	e.Mu.Unlock()
	if err != nil {
		scope.Finish(err)
		t.Fatal(err)
	}
	runtime := core.NewCatalogRuntime(core.CatalogRuntimeFields{Engine: e, Auth: auth, Settings: s.Settings})
	runtime.TableStatistics(ref.Address)
	if err := scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
}
func TestStatsResetSharedCacheLifetime(t *testing.T) {
	e, s, peer, ps, key, pk := sharedStatsFixture(t)
	sharedStatsSeed(e, key)
	sharedStatsSeed(peer, pk)
	sharedStatsBackend(e, s, 11, 13)
	statsFunctionExec(t, s, "SET stats_fetch_consistency=snapshot", "BEGIN")
	sharedStatsCapture(t, e, s, key.Object)
	owned := reflect.ValueOf(core.PayloadOf(s)).Elem().FieldByName("statsReader")
	fixed := owned.FieldByName("shared").Index(int(statistics.Archiver))
	if fixed.FieldByName("Counters").Index(0).Int() != 11 || !owned.FieldByName("full").Bool() {
		t.Fatal("physical first relation access missed fixed capture")
	}
	backend := owned.FieldByName("backends").MapIndex(reflect.ValueOf(s.Token()))
	if !backend.IsValid() || backend.FieldByName("fact").FieldByName("IO").FieldByName("Operations").Int() != 11 {
		t.Fatal("physical snapshot missed backend")
	}
	stamp := owned.FieldByName("stamp")
	if stamp.IsZero() {
		t.Fatal("physical timestamp missing")
	}
	stampWall, stampExt, stampLoc := stamp.FieldByName("wall").Uint(), stamp.FieldByName("ext").Int(), stamp.FieldByName("loc").Pointer()
	resetStatsExec(t, s, "SELECT pg_stat_reset_shared('archiver')")
	resetStatsExec(t, ps, "SELECT pg_stat_reset_shared('io')")
	resetStatsExec(t, s, "SELECT pg_stat_reset_backend_stats(pg_backend_pid())")
	retainedBackend := owned.FieldByName("backends").MapIndex(reflect.ValueOf(s.Token()))
	if !retainedBackend.IsValid() || retainedBackend.FieldByName("fact").FieldByName("IO").FieldByName("Operations").Int() != 11 || retainedBackend.FieldByName("fact").FieldByName("WAL").FieldByName("Operations").Int() != 13 {
		t.Fatal("own backend reset invalidated physical capture")
	}
	if current, ok := e.Statistics.ReadBackend(s.Token()); !ok || current.IO.Operations != 0 || current.WAL.Operations != 0 {
		t.Fatal("own backend published reset absent", current, ok)
	}
	if fixed.FieldByName("Counters").Index(0).Int() != 11 || stamp.FieldByName("wall").Uint() != stampWall || stamp.FieldByName("ext").Int() != stampExt || stamp.FieldByName("loc").Pointer() != stampLoc {
		t.Fatal("own/peer reset invalidated actual reader")
	}
	resetStatsExec(t, s, "SELECT pg_stat_clear_snapshot()")
	if owned.FieldByName("full").Bool() || owned.FieldByName("kinds").Uint() != 0 || owned.FieldByName("backends").Len() != 0 || !owned.FieldByName("stamp").IsZero() {
		t.Fatal("clear retained partitions")
	}
	sharedStatsCapture(t, e, s, key.Object)
	if owned.FieldByName("shared").Index(int(statistics.Archiver)).FieldByName("Counters").Index(0).Int() != 0 {
		t.Fatal("clear did not expose reset")
	}
	statsFunctionExec(t, s, "SET stats_fetch_consistency=none", "SET stats_fetch_consistency=snapshot", "SELECT pg_stat_get_snapshot_timestamp() IS NULL")
	if owned.FieldByName("full").Bool() || owned.FieldByName("kinds").Uint() != 0 || owned.FieldByName("backends").Len() != 0 || !owned.FieldByName("stamp").IsZero() {
		t.Fatal("settings generation roundtrip retained typed capture")
	}
	sharedStatsCapture(t, e, s, key.Object)
	statsFunctionExec(t, s, "ROLLBACK")
	if owned.FieldByName("full").Bool() || !owned.FieldByName("stamp").IsZero() {
		t.Fatal("terminal retained reader")
	}
	for _, commands := range [][]string{{"BEGIN", "SELECT pg_stat_reset_shared('wal')", "ROLLBACK"}, {"BEGIN", "SAVEPOINT reset_shared_mark", "SELECT pg_stat_reset_shared('wal')", "ROLLBACK TO reset_shared_mark", "ROLLBACK"}} {
		sharedStatsSeed(e, key)
		for _, q := range commands {
			if q == "SELECT pg_stat_reset_shared('wal')" {
				resetStatsExec(t, s, q)
			} else {
				statsFunctionExec(t, s, q)
			}
		}
		wal := key
		wal.Category = statistics.WAL
		if f := e.Statistics.Read(wal); f.Operations != 0 || f.ResetAt.IsZero() {
			t.Fatal("rollback undid shared reset", f)
		}
	}
	for _, finish := range []string{"COMMIT", "ROLLBACK"} {
		sharedStatsSeed(e, key)
		statsFunctionExec(t, s, "BEGIN")
		resetStatsExec(t, s, "SELECT pg_stat_reset_shared('wal')")
		statsFunctionExec(t, s, "PREPARE TRANSACTION 'shared_reset_prepared'", finish+" PREPARED 'shared_reset_prepared'")
		wal := key
		wal.Category = statistics.WAL
		if f := e.Statistics.Read(wal); f.Operations != 0 || f.ResetAt.IsZero() {
			t.Fatal("prepared completion undid published reset", finish, f)
		}
	}
	// Existing actual session publication gate: a function call in an explicit
	// transaction is retained, then appears only at the ordinary terminal drain.
	statsFunctionExec(t, s, "SET track_functions='all'", "CREATE FUNCTION shared_pending_fn() RETURNS integer LANGUAGE plpgsql AS 'BEGIN RETURN 7; END'", "BEGIN", "SELECT shared_pending_fn()")
	function := statsFunctionKey(t, e, "shared_pending_fn")
	pending := reflect.ValueOf(core.PayloadOf(s)).Elem().FieldByName("statsWriter").FieldByName("pending").MapIndex(reflect.ValueOf(function))
	if !pending.IsValid() || pending.FieldByName("Calls").Int() != 1 {
		t.Fatal("genuine session event not pending")
	}
	resetStatsExec(t, ps, "SELECT pg_stat_reset_shared(NULL)")
	if e.Statistics.Read(function).Calls != 0 {
		t.Fatal("reset flushed peer writer")
	}
	statsFunctionExec(t, s, "COMMIT")
	if e.Statistics.Read(function).Calls != 1 {
		t.Fatal("ordinary terminal lost pending function control")
	}
}
