package logical

import (
	"fmt"
	"slices"
	"testing"
)

const slotRow = "SELECT plugin, slot_type, database = current_database(), datoid = (SELECT oid FROM pg_database WHERE datname = current_database()), temporary, active, active_pid IS NULL, xmin IS NULL, catalog_xmin IS NOT NULL, restart_lsn <= confirmed_flush_lsn, wal_status, safe_wal_size, two_phase, two_phase_at, inactive_since IS NOT NULL, conflicting, invalidation_reason, failover, synced FROM pg_replication_slots WHERE slot_name = "

// Creation publishes one usable slot whose returned position is its
// confirmed position; every failed creation leaves no slot behind.
func TestLogicalStreamSlotCreation(t *testing.T) {
	f := newFixture(t)
	s := f.session()
	rows := f.must(s, "SELECT * FROM pg_create_logical_replication_slot('made', 'recorder', failover => true)")
	if len(rows) != 1 || rows[0][0] != "made" || rows[0][1] == nil {
		t.Fatal("creation result", rows)
	}
	if got := f.value(s, "SELECT confirmed_flush_lsn::text FROM pg_replication_slots WHERE slot_name = 'made'"); got != rows[0][1] {
		t.Fatal("returned position", got, rows[0][1])
	}
	want := []any{"recorder", "logical", "t", "t", "f", "f", "t", "t", "t", "t", "reserved", nil, "f", nil, "t", "f", nil, "t", "f"}
	if got := f.must(s, slotRow+"'made'"); !slices.Equal(got[0], want) {
		t.Fatalf("inventory\n got %v\nwant %v", got[0], want)
	}
	if got := f.value(s, "SELECT count(*) FROM pg_stat_replication_slots WHERE slot_name = 'made' AND total_txns = 0 AND stats_reset IS NULL"); got != "1" {
		t.Fatal("statistics row", got)
	}
	f.must(s, "SELECT pg_stat_reset_replication_slot('made')")
	if got := f.value(s, "SELECT stats_reset IS NOT NULL FROM pg_stat_replication_slots WHERE slot_name = 'made'"); got != "t" {
		t.Fatal("statistics reset", got)
	}
	f.fails(s, "SELECT pg_create_logical_replication_slot('made', 'recorder')", "42710", `replication slot "made" already exists`)
	f.fails(s, "SELECT pg_create_logical_replication_slot('Bad', 'recorder')", "42602", `replication slot name "Bad" contains invalid character`)
	f.fails(s, "SELECT pg_create_logical_replication_slot('tf', 'recorder', true, false, true)", "0A000", "cannot enable failover for a temporary replication slot")
	f.fails(s, "SELECT pg_create_logical_replication_slot('nolib', 'missing')", "58P01", `could not access file "missing": No such file or directory`)
	f.fails(s, "SELECT pg_create_logical_replication_slot('denied', 'test_decoding')", "42501", `library "test_decoding" may not be used as an output plugin`)
	f.must(s, "CREATE TABLE t (id int)")
	f.must(s, "BEGIN")
	f.must(s, "INSERT INTO t VALUES (1)")
	f.fails(s, "SELECT pg_create_logical_replication_slot('written', 'recorder')", "25001", "cannot create logical replication slot in transaction that has performed writes")
	f.must(s, "ROLLBACK")
	for i := range 9 {
		f.slot(s, fmt.Sprintf("fill%d", i))
	}
	d := f.fails(s, "SELECT pg_create_logical_replication_slot('full', 'recorder')", "53400", "all replication slots are in use")
	if d.Hint != `Free one or increase "max_replication_slots".` {
		t.Fatal("capacity hint", d.Hint)
	}
	if got := f.value(s, "SELECT string_agg(slot_name, ',' ORDER BY slot_name) FROM pg_replication_slots WHERE slot_name NOT LIKE 'fill%'"); got != "made" {
		t.Fatal("failed creations left slots", got)
	}
	starts, stops := f.probe.counts()
	if starts != stops {
		t.Fatal("plugin contexts left open", starts, stops)
	}
}

// Peek repeats, get consumes once, and copies and independent slots keep
// their own positions.
func TestLogicalStreamConsumption(t *testing.T) {
	f := newFixture(t)
	s := f.session()
	f.must(s, "CREATE TABLE t (id int)")
	f.slot(s, "main")
	f.slot(s, "other")
	f.must(s, "INSERT INTO t VALUES (1)")
	first := tx(row("1"))
	f.expect(f.peek(s, "main"), first...)
	f.expect(f.peek(s, "main"), first...)
	f.must(s, "SELECT pg_copy_logical_replication_slot('main', 'copy')")
	f.expect(f.changes(s, "main"), first...)
	f.expect(f.changes(s, "main"))
	f.must(s, "INSERT INTO t VALUES (2)")
	second := tx(row("2"))
	f.expect(f.changes(s, "main"), second...)
	f.expect(f.changes(s, "copy"), concat(first, second)...)
	f.expect(f.changes(s, "other"), concat(first, second)...)
	if got := f.value(s, "SELECT count(DISTINCT confirmed_flush_lsn) FROM pg_replication_slots"); got != "1" {
		t.Fatal("consumed positions", got)
	}
	// A dropped name never revives its predecessor's state.
	f.must(s, "SELECT pg_drop_replication_slot('main')")
	f.fails(s, "SELECT * FROM pg_logical_slot_get_changes('main', NULL, NULL)", "42704", `replication slot "main" does not exist`)
	f.slot(s, "main")
	f.must(s, "INSERT INTO t VALUES (3)")
	f.expect(f.changes(s, "main"), tx(row("3"))...)
}

// Positions are ordered stream progress shared by rows, slots and limits.
func TestLogicalStreamPositions(t *testing.T) {
	f := newFixture(t)
	s := f.session()
	f.must(s, "CREATE TABLE t (id int)")
	f.slot(s, "pos")
	start := f.value(s, "SELECT confirmed_flush_lsn::text FROM pg_replication_slots WHERE slot_name = 'pos'")
	f.must(s, "BEGIN")
	xid := f.value(s, "SELECT pg_current_xact_id()::text::bigint % 4294967296")
	f.must(s, "INSERT INTO t VALUES (1), (2)")
	f.must(s, "COMMIT")
	f.must(s, "INSERT INTO t VALUES (3)")
	rows := f.must(s, "SELECT lsn::text, xid::text, data, lsn >= '"+start.(string)+"' FROM pg_logical_slot_peek_changes('pos', NULL, NULL)")
	if len(rows) != 7 || rows[0][1] != xid || rows[4][1] == xid {
		t.Fatal("rows and transaction identity", rows, xid)
	}
	for i := 1; i < len(rows); i++ {
		if rows[i][3] != "t" || f.value(s, "SELECT '"+rows[i-1][0].(string)+"'::pg_lsn <= '"+rows[i][0].(string)+"'::pg_lsn") != "t" {
			t.Fatal("positions are not increasing", rows)
		}
	}
	commit := rows[3][0].(string)
	// upto_lsn includes exactly the transactions committing before it.
	f.expect(f.data(s, "pg_logical_slot_get_changes('pos', '"+commit+"', NULL)"), tx(row("1"), row("2"))...)
	if got := f.value(s, "SELECT confirmed_flush_lsn::text FROM pg_replication_slots WHERE slot_name = 'pos'"); got != commit {
		t.Fatal("get confirms the end of the last record", got, commit)
	}
	f.expect(f.data(s, "pg_logical_slot_get_changes('pos', '0/0', NULL)"), tx(row("3"))...)

	// Change limits apply after whole transactions; negative stops after the
	// first record, zero and NULL are unlimited.
	for _, id := range []string{"4", "5", "6"} {
		f.must(s, "INSERT INTO t VALUES ("+id+")")
	}
	f.expect(f.data(s, "pg_logical_slot_peek_changes('pos', NULL, -1)"))
	f.expect(f.data(s, "pg_logical_slot_get_changes('pos', NULL, 1)"), tx(row("4"))...)
	f.expect(f.data(s, "pg_logical_slot_get_changes('pos', NULL, 4)"), concat(tx(row("5")), tx(row("6")))...)
	f.must(s, "INSERT INTO t VALUES (7)")
	f.expect(f.data(s, "pg_logical_slot_get_changes('pos', NULL, 0)"), tx(row("7"))...)
}

// Advance confirms an actual reached position, never backward or past the
// end of the stream.
func TestLogicalStreamAdvance(t *testing.T) {
	f := newFixture(t)
	s := f.session()
	f.must(s, "CREATE TABLE t (id int)")
	f.slot(s, "adv")
	f.must(s, "INSERT INTO t VALUES (1)")
	rows := f.must(s, "SELECT * FROM pg_replication_slot_advance('adv', 'FFFFFFFF/FFFFFFFF')")
	if len(rows) != 1 || rows[0][0] != "adv" || rows[0][1] != f.value(s, "SELECT confirmed_flush_lsn::text FROM pg_replication_slots WHERE slot_name = 'adv'") {
		t.Fatal("advance result", rows)
	}
	end := rows[0][1].(string)
	f.expect(f.changes(s, "adv"))
	if got := f.must(s, "SELECT end_lsn::text FROM pg_replication_slot_advance('adv', '"+end+"')"); got[0][0] != end {
		t.Fatal("advance to the same position", got)
	}
	f.fails(s, "SELECT pg_replication_slot_advance('adv', '0/1')", "55000", "cannot advance replication slot to 0/1, minimum is "+end)
	f.fails(s, "SELECT pg_replication_slot_advance('adv', '0/0')", "22023", "invalid target WAL LSN")
	f.must(s, "INSERT INTO t VALUES (2)")
	f.expect(f.changes(s, "adv"), tx(row("2"))...)
}

// Decoding calls validate in PostgreSQL's order and keep plugin errors.
func TestLogicalStreamDecodingDiagnostics(t *testing.T) {
	f := newFixture(t)
	s := f.session()
	f.slot(s, "diag")
	f.fails(s, "SELECT * FROM pg_logical_slot_get_changes(NULL, NULL, NULL)", "22004", "slot name must not be null")
	f.fails(s, "SELECT * FROM pg_logical_slot_get_changes('diag', NULL, NULL, 'binary')", "0A000", "array must have even number of elements")
	f.fails(s, "SELECT * FROM pg_logical_slot_get_changes('absent', NULL, NULL)", "42704", `replication slot "absent" does not exist`)
	f.fails(s, "SELECT * FROM pg_logical_slot_peek_changes('diag', NULL, NULL, 'binary', '1')", "0A000", `logical decoding output plugin "recorder" produces binary output, but function "pg_logical_slot_peek_changes(name,pg_lsn,integer,text[])" expects textual data`)
	f.must(s, "SELECT * FROM pg_logical_slot_peek_binary_changes('diag', NULL, NULL, 'binary', '1')")
	f.fails(s, "SELECT * FROM pg_logical_slot_peek_changes('diag', NULL, NULL, 'nope', 'x')", "22023", `option "nope" = "x" is unknown`)
	f.fails(s, "SELECT * FROM pg_logical_slot_peek_changes('diag', NULL, NULL, 'fail', 'startup')", "XX000", "recorder: startup failure")
	f.fails(s, "SELECT * FROM pg_logical_slot_peek_changes('diag', NULL, NULL, 'rewrites', '1')", "0A000", `logical decoding output plugin "recorder" requested table rewrite changes, which are not supported`)
	f.must(s, "CREATE TABLE t (id int)")
	f.must(s, "INSERT INTO t VALUES (1)")
	// Streaming is honoured only where PostgreSQL would stream nothing.
	f.expect(f.peek(s, "diag", "stream", "1"), concat(tx(), tx(row("1")))...)
	f.must(s, "SET debug_logical_replication_streaming = immediate")
	f.fails(s, "SELECT * FROM pg_logical_slot_peek_changes('diag', NULL, NULL, 'stream', '1')", "0A000", `logical decoding output plugin "recorder" requires streaming of in-progress transactions, which is not supported`)
	f.must(s, "RESET debug_logical_replication_streaming")
	f.expect(f.data(s, "pg_logical_slot_peek_changes('diag', NULL, NULL, VARIADIC ARRAY['local', '1'])"), concat(tx(), tx(row("1")))...)
	binary := f.must(s, "SELECT data FROM pg_logical_slot_get_binary_changes('diag', NULL, NULL)")
	if len(binary) != 5 || binary[0][0] != `\x424547494e` {
		t.Fatal("binary rows", binary)
	}
	starts, stops := f.probe.counts()
	if starts != stops {
		t.Fatal("plugin contexts left open", starts, stops)
	}
}
