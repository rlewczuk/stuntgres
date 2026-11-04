package logical

import "testing"

func row(id string) string { return "INSERT public.t (id[integer]:'" + id + "')" }

// Only committed effects reach the stream; undone statements, savepoints and
// exception blocks disappear while their successful neighbours keep order.
func TestLogicalStreamTransactions(t *testing.T) {
	f := newFixture(t)
	s := f.session()
	f.must(s, "CREATE TABLE t (id int PRIMARY KEY)")
	f.must(s, "CREATE TABLE side (id int)")
	f.must(s, "CREATE FUNCTION side_then_fail() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO side VALUES (NEW.id); IF NEW.id = 13 THEN RAISE EXCEPTION 'boom'; END IF; RETURN NEW; END$$")
	f.must(s, "CREATE TRIGGER t_side BEFORE INSERT ON t FOR EACH ROW EXECUTE FUNCTION side_then_fail()")
	f.slot(s, "txn")

	f.fails(s, "INSERT INTO t VALUES (13)", "P0001", "boom") // Autocommit failure, after a trigger write.
	f.must(s, "INSERT INTO t VALUES (1)")
	f.fails(s, "INSERT INTO t VALUES (1)", "23505", `duplicate key value violates unique constraint "t_pkey"`)

	f.must(s, "BEGIN")
	f.must(s, "INSERT INTO t VALUES (2)")
	f.must(s, "ROLLBACK")

	f.must(s, "BEGIN")
	f.must(s, "INSERT INTO t VALUES (3)")
	f.fails(s, "INSERT INTO t VALUES (13)", "P0001", "boom")
	f.must(s, "COMMIT") // Commits nothing: the block failed.

	f.must(s, "BEGIN")
	f.must(s, "INSERT INTO t VALUES (4)")
	f.must(s, "SAVEPOINT a")
	f.must(s, "INSERT INTO t VALUES (5)")
	f.must(s, "SAVEPOINT b")
	f.must(s, "INSERT INTO t VALUES (6)")
	f.must(s, "RELEASE b")
	f.must(s, "ROLLBACK TO a")
	f.must(s, "INSERT INTO t VALUES (7)")
	f.must(s, "SAVEPOINT c")
	f.must(s, "INSERT INTO t VALUES (8)")
	f.fails(s, "INSERT INTO t VALUES (13)", "P0001", "boom")
	f.must(s, "ROLLBACK TO c")
	f.must(s, "INSERT INTO t VALUES (9)")
	f.must(s, "COMMIT")

	f.must(s, `DO $$BEGIN
		INSERT INTO t VALUES (10);
		BEGIN INSERT INTO t VALUES (11); INSERT INTO t VALUES (13); EXCEPTION WHEN OTHERS THEN PERFORM 1; END;
		INSERT INTO t VALUES (12);
	END$$`)

	f.must(s, "BEGIN READ ONLY")
	f.must(s, "SELECT count(*) FROM t")
	f.must(s, "COMMIT")
	f.must(s, "BEGIN")
	f.must(s, "COMMIT")
	f.must(s, "SELECT pg_current_xact_id()") // An XID alone decodes nothing.

	f.must(s, "BEGIN")
	f.must(s, "CREATE TABLE ddl_only (id int)")
	f.must(s, "COMMIT")

	f.expect(f.changes(s, "txn"), concat(
		tx("INSERT public.side (id[integer]:'1')", row("1")),
		tx("INSERT public.side (id[integer]:'4')", row("4"), "INSERT public.side (id[integer]:'7')", row("7"), "INSERT public.side (id[integer]:'9')", row("9")),
		tx("INSERT public.side (id[integer]:'10')", row("10"), "INSERT public.side (id[integer]:'12')", row("12")),
		tx(), // Catalog-only transaction.
	)...)
}

// Ordinary decoding returns transactions in commit order, never interleaved.
func TestLogicalStreamCommitOrder(t *testing.T) {
	f := newFixture(t)
	a, b := f.session(), f.session()
	f.must(a, "CREATE TABLE t (id int)")
	f.slot(a, "order")
	f.must(a, "BEGIN")
	f.must(a, "INSERT INTO t VALUES (1)")
	f.must(b, "BEGIN")
	f.must(b, "INSERT INTO t VALUES (2)")
	f.must(a, "INSERT INTO t VALUES (3)")
	f.expect(f.peek(b, "order")) // Nothing committed yet.
	f.must(b, "COMMIT")
	f.must(a, "COMMIT")
	f.expect(f.changes(a, "order"), concat(tx(row("2")), tx(row("1"), row("3")))...)
}

// Prepared transactions decode at COMMIT PREPARED on an ordinary slot, and at
// PREPARE with a separate finish on a two-phase slot.
func TestLogicalStreamPreparedTransactions(t *testing.T) {
	f := newFixture(t)
	s := f.session()
	f.must(s, "CREATE TABLE t (id int)")
	f.slot(s, "plain2pc")
	f.must(s, "SELECT pg_create_logical_replication_slot('twophase', 'recorder', false, true)")
	f.must(s, "SELECT pg_create_logical_replication_slot('notsupported', 'plain', false, true)")
	for _, gid := range []string{"g1", "g2"} {
		f.must(s, "BEGIN")
		f.must(s, "INSERT INTO t VALUES (1)")
		f.must(s, "PREPARE TRANSACTION '"+gid+"'")
	}
	f.expect(f.changes(s, "plain2pc"))
	f.expect(f.changes(s, "twophase"), "BEGIN PREPARE g1", row("1"), "PREPARE g1", "BEGIN PREPARE g2", row("1"), "PREPARE g2")
	f.expect(f.changes(s, "notsupported"))
	f.must(s, "COMMIT PREPARED 'g1'")
	f.must(s, "ROLLBACK PREPARED 'g2'")
	f.expect(f.changes(s, "plain2pc"), tx(row("1"))...)
	f.expect(f.changes(s, "twophase"), "COMMIT PREPARED g1", "ROLLBACK PREPARED g2")
	f.expect(f.changes(s, "notsupported"), tx(row("1"))...)
	if got := f.value(s, "SELECT two_phase_at IS NOT NULL AND two_phase_at <= confirmed_flush_lsn FROM pg_replication_slots WHERE slot_name = 'twophase'"); got != "t" {
		t.Fatal("two_phase_at", got)
	}
	if got := f.value(s, "SELECT two_phase_at IS NULL FROM pg_replication_slots WHERE slot_name = 'plain2pc'"); got != "t" {
		t.Fatal("ordinary two_phase_at", got)
	}
}
