package logical

import (
	"context"
	"testing"

	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/session"
)

func (f *fixture) slotExists(s *session.Session, name string) bool {
	f.t.Helper()
	return f.value(s, "SELECT count(*) FROM pg_replication_slots WHERE slot_name = '"+name+"'") == "1"
}

func endSession(t *testing.T, s *session.Session) {
	t.Helper()
	s.RequestEnd(session.CleanClose)
	if err := s.WaitEnd(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// A temporary slot belongs to its session: active for it, busy for others,
// dropped at session end and at any top-level error, including parse errors.
func TestLogicalStreamTemporarySlots(t *testing.T) {
	f := newFixture(t)
	owner, peer := f.session(), f.session()
	f.must(owner, "CREATE TABLE t (id int)")
	f.must(owner, "SELECT pg_create_logical_replication_slot('tmp', 'recorder', true)")
	pid := f.value(owner, "SELECT pg_backend_pid()::text")
	if got := f.must(peer, "SELECT temporary, active, active_pid::text, inactive_since FROM pg_replication_slots WHERE slot_name = 'tmp'"); len(got) != 1 || got[0][0] != "t" || got[0][1] != "t" || got[0][2] != pid || got[0][3] != nil {
		t.Fatal("temporary inventory", got, pid)
	}
	busy := `replication slot "tmp" is active for PID ` + pid.(string)
	f.fails(peer, "SELECT * FROM pg_logical_slot_peek_changes('tmp', NULL, NULL)", "55006", busy)
	f.fails(peer, "SELECT pg_drop_replication_slot('tmp')", "55006", busy)
	f.fails(peer, "SELECT pg_replication_slot_advance('tmp', 'FFFFFFFF/0')", "55006", busy)
	f.must(owner, "INSERT INTO t VALUES (1)")
	f.expect(f.changes(owner, "tmp"), tx(row("1"))...)
	// A copy reads the source without acquiring it.
	f.must(peer, "SELECT pg_copy_logical_replication_slot('tmp', 'tmp_copy', false)")
	f.must(owner, "BEGIN")
	f.must(owner, "SELECT 1")
	f.must(owner, "ROLLBACK") // SQL ROLLBACK does not undo slot state.
	if !f.slotExists(peer, "tmp") {
		t.Fatal("rollback dropped the temporary slot")
	}
	f.fails(owner, "SELECT 1/0", "22012", "division by zero")
	if f.slotExists(peer, "tmp") || !f.slotExists(peer, "tmp_copy") {
		t.Fatal("top-level error must drop only the session's temporary slots")
	}
	f.must(owner, "SELECT pg_create_logical_replication_slot('tmp', 'recorder', true)")
	if _, err := run(owner, "SELEC 1"); err == nil {
		t.Fatal("parse error expected")
	}
	if f.slotExists(peer, "tmp") {
		t.Fatal("parse error must drop the temporary slot")
	}
	// An error caught inside the session's own exception block is not
	// top-level error recovery.
	f.must(owner, "SELECT pg_create_logical_replication_slot('tmp', 'recorder', true)")
	f.must(owner, "DO $$BEGIN PERFORM 1/0; EXCEPTION WHEN division_by_zero THEN PERFORM 1; END$$")
	if !f.slotExists(peer, "tmp") {
		t.Fatal("caught error dropped the temporary slot")
	}
	// A failure of a multi-statement message's own commit is a top-level error.
	f.must(owner, "CREATE TABLE p (id int PRIMARY KEY)")
	f.must(owner, "CREATE TABLE c (id int REFERENCES p DEFERRABLE INITIALLY DEFERRED)")
	if err := runBatch(owner, "SELECT pg_create_logical_replication_slot('tmp_batch', 'recorder', true); INSERT INTO c VALUES (1)"); err == nil {
		t.Fatal("deferred constraint violation expected")
	}
	if f.slotExists(peer, "tmp_batch") || f.slotExists(peer, "tmp") {
		t.Fatal("message commit failure must drop every temporary slot of the session")
	}
	f.must(owner, "SELECT pg_create_logical_replication_slot('tmp', 'recorder', true)")
	endSession(t, owner)
	if f.slotExists(peer, "tmp") {
		t.Fatal("session end must drop the temporary slot")
	}
}

// Persistent slots outlive their sessions; a slot belongs to one database of
// its own cluster only.
func TestLogicalStreamSlotScope(t *testing.T) {
	f := newFixture(t)
	s := enginetest.StartSession(t, f.e, nil)
	f.must(s, "SET output_plugin_libraries = recorder")
	f.must(s, "CREATE TABLE t (id int)")
	f.slot(s, "kept")
	f.must(s, "INSERT INTO t VALUES (1)")
	endSession(t, s)
	s = f.session()
	if got := f.value(s, "SELECT active FROM pg_replication_slots WHERE slot_name = 'kept'"); got != "f" {
		t.Fatal("persistent slot after session end", got)
	}
	f.expect(f.changes(s, "kept"), tx(row("1"))...)

	f.must(s, "CREATE DATABASE other")
	target := f.e.Cluster.Targets["other"]
	if target == nil {
		t.Fatal("missing database")
	}
	o := enginetest.StartSession(t, target, nil)
	f.must(o, "SET output_plugin_libraries = recorder")
	f.fails(o, "SELECT * FROM pg_logical_slot_get_changes('kept', NULL, NULL)", "55000", `replication slot "kept" was not created in this database`)
	f.must(o, "CREATE TABLE t (id int)")
	f.slot(o, "theirs")
	f.must(o, "INSERT INTO t VALUES (2)")
	f.must(s, "INSERT INTO t VALUES (3)")
	f.expect(f.changes(o, "theirs"), tx(row("2"))...)
	f.expect(f.changes(s, "kept"), tx(row("3"))...)
	if got := f.value(s, "SELECT database FROM pg_replication_slots WHERE slot_name = 'theirs'"); got != "other" {
		t.Fatal("database association", got)
	}
	f.must(s, "SELECT pg_drop_replication_slot('theirs')") // Any database may drop it.

	// A copy made in another database starts from its source's position in
	// that database's history, which the cluster-wide stream retains.
	f.must(s, "SELECT pg_replication_slot_advance('kept', 'FFFFFFFF/0')")
	f.must(o, "INSERT INTO t VALUES (4)")
	f.must(o, "SELECT pg_copy_logical_replication_slot('kept', 'copied_here')")
	f.expect(f.changes(o, "copied_here"), tx(row("4"))...)
	f.must(o, "SELECT pg_drop_replication_slot('copied_here')")

	// Another cluster has independent slots and history.
	g := newFixture(t)
	gs := g.session()
	g.fails(gs, "SELECT * FROM pg_logical_slot_peek_changes('kept', NULL, NULL)", "42704", `replication slot "kept" does not exist`)
	g.slot(gs, "kept")
	g.expect(g.changes(gs, "kept"))
}

// The cluster's wal_level governs every logical operation.
func TestLogicalStreamWALLevel(t *testing.T) {
	e := enginetest.StartSession(t, newReplicaEngine(), nil)
	f := &fixture{t: t}
	if got := f.value(e, "SHOW wal_level"); got != "replica" {
		t.Fatal("default wal_level", got)
	}
	required := `logical decoding requires "wal_level" >= "logical"`
	f.fails(e, "SELECT pg_create_logical_replication_slot('s', 'test_decoding')", "55000", required)
	f.fails(e, "SELECT pg_copy_logical_replication_slot('s', 'd')", "55000", required)
	f.fails(e, "SELECT * FROM pg_logical_slot_peek_changes('s', NULL, NULL)", "55000", required)
	f.fails(e, "SELECT pg_drop_replication_slot('s')", "42704", `replication slot "s" does not exist`)
	f.fails(e, "SELECT pg_replication_slot_advance('s', '0/1')", "42704", `replication slot "s" does not exist`)
}
