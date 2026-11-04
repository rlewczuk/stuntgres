package reference_test

import (
	"bytes"
	"database/sql"
	"reflect"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func statIOClear(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	resetReplicationVoid(t, p, b, c, "SELECT pg_stat_clear_snapshot()", "pg_stat_clear_snapshot")
}

func statIOStamp(t *testing.T, rows []statIORow) time.Time {
	t.Helper()
	if len(rows) != 79 { t.Fatal("reset row count", len(rows)) }
	at, ok := rows[0][19].(time.Time)
	if !ok || at.IsZero() { t.Fatal("reset event must be non-NULL", rows[0][19]) }
	for _, row := range rows {
		stamp, ok := row[19].(time.Time)
		if !ok || !stamp.Equal(at) { t.Fatal("common reset including WAL", row) }
	}
	return at
}

func statIOFresh(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) []statIORow {
	t.Helper()
	statIOClear(t, p, b, c)
	rows := statIOQuery(t, p, b, c, statIOSelect)
	statIOCheck(t, rows, b.PG)
	return rows
}

// SQL round trips usually separate microseconds already. Retry actual reset
// events until the clock advances, bounded by the scenario context, not sleep.
func statIOResetAfter(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, call string, before time.Time) time.Time {
	t.Helper()
	for {
		if err := p.Ctx.Err(); err != nil { t.Fatal("waiting for reset timestamp progress", err) }
		resetReplicationVoid(t, p, b, c, "SELECT "+call, "pg_stat_reset_shared")
		after := statIOStamp(t, statIOFresh(t, p, b, c))
		if after.After(before) { return after }
	}
}

func TestReferencePGStatIOReset(t *testing.T) {
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		peer, err := b.DB.Conn(p.Ctx)
		if err != nil { t.Fatal(err) }
		defer peer.Close()
		first := statIOResetAfter(t, p, b, peer, "pg_stat_reset_shared('io')", time.Time{})
		before := statIOFresh(t, p, b, c)
		if !statIOStamp(t, before).Equal(first) { t.Fatal("published IO reset") }
		repeated := statIOQuery(t, p, b, c, statIOSelect)
		statIOCheck(t, repeated, b.PG)
		if !b.PG && !reflect.DeepEqual(before, repeated) { t.Fatal("reads fabricated IO events") }
		later := statIOResetAfter(t, p, b, peer, "pg_stat_reset_shared('io')", first)
		if !statIOStamp(t, statIOFresh(t, p, b, c)).Equal(later) { t.Fatal("fresh peer reset") }
		resetReplicationVoid(t, p, b, peer, "SELECT pg_stat_reset_shared('wal')", "pg_stat_reset_shared")
		if !statIOStamp(t, statIOFresh(t, p, b, c)).Equal(later) { t.Fatal("WAL reset changed IO family") }
		for _, call := range []string{"pg_stat_reset_shared(NULL)", "pg_stat_reset_shared()"} {
			later = statIOResetAfter(t, p, b, peer, call, later)
			if !statIOStamp(t, statIOFresh(t, p, b, c)).Equal(later) { t.Fatal("all-shared reset omitted IO") }
		}
		if !b.PG {
			for _, query := range []string{"CREATE TABLE io_logical(v integer)", "INSERT INTO io_logical VALUES(1),(2)", "UPDATE io_logical SET v=3 WHERE v=1", "DELETE FROM io_logical WHERE v=2", "CREATE SEQUENCE io_logical_seq"} {
				activityExec(t, p, c, query)
			}
			var value int64
			if err := c.QueryRowContext(p.Ctx, "SELECT nextval('io_logical_seq')").Scan(&value); err != nil || value != 1 { t.Fatal(value, err) }
			if err := c.QueryRowContext(p.Ctx, "SELECT setval('io_logical_seq',37)").Scan(&value); err != nil || value != 37 { t.Fatal(value, err) }
			rows := statIOFresh(t, p, b, c)
			if !statIOStamp(t, rows).Equal(later) { t.Fatal("logical events changed IO reset") }
		}
	})
}

func TestReferencePGStatIOConsistency(t *testing.T) {
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		peer, err := b.DB.Conn(p.Ctx)
		if err != nil { t.Fatal(err) }
		defer peer.Close()
		activityExec(t, p, c, "CREATE TABLE io_snapshot_anchor(v integer)")
		for _, mode := range []string{"none", "cache", "snapshot"} {
			for _, prepared := range []bool{false, true} {
				activityExec(t, p, c, "SET stats_fetch_consistency='"+mode+"'")
				first := statIOResetAfter(t, p, b, peer, "pg_stat_reset_shared('io')", time.Time{})
				activityExec(t, p, c, "BEGIN")
				var e fixture.Executor = c
				var stmt *sql.Stmt
				if prepared {
					stmt, err = c.PrepareContext(p.Ctx, statIOSelect)
					if err != nil { t.Fatal(err) }
					e = fixture.HeldStatement{Executor:c, Statement:stmt, SQL:statIOSelect}
				}
				// Prepare is not a read. A reset before first execution is visible.
				first = statIOResetAfter(t, p, b, peer, "pg_stat_reset_shared('io')", first)
				rows := statIOQuery(t, p, b, e, statIOSelect)
				statIOCheck(t, rows, b.PG)
				if !statIOStamp(t, rows).Equal(first) { t.Fatal("Prepare captured IO") }
				var snapshot sql.NullTime
				if err := c.QueryRowContext(p.Ctx, "SELECT pg_stat_get_snapshot_timestamp()").Scan(&snapshot); err != nil || snapshot.Valid != (mode=="snapshot") { t.Fatal("IO starts only full snapshots", mode, snapshot, err) }
				later := statIOResetAfter(t, p, b, peer, "pg_stat_reset_shared('io')", first)
				want := first
				if mode=="none" { want=later }
				if !statIOStamp(t, statIOQuery(t, p, b, e, statIOSelect)).Equal(want) { t.Fatal("transaction IO reader ownership", mode) }
				statIOClear(t, p, b, c)
				if !statIOStamp(t, statIOQuery(t, p, b, e, statIOSelect)).Equal(later) { t.Fatal("clear did not release IO cut", mode) }
				activityExec(t, p, c, "COMMIT")
				if stmt != nil { stmt.Close() }
			}
		}
		activityExec(t, p, c, "SET stats_fetch_consistency='snapshot'")
		first := statIOResetAfter(t, p, b, peer, "pg_stat_reset_shared('io')", time.Time{})
		activityExec(t, p, c, "BEGIN")
		var inserts int64
		if err := c.QueryRowContext(p.Ctx, "SELECT n_tup_ins FROM pg_stat_all_tables WHERE relname='io_snapshot_anchor'").Scan(&inserts); err != nil || inserts != 0 { t.Fatal(inserts, err) }
		later := statIOResetAfter(t, p, b, peer, "pg_stat_reset_shared('io')", first)
		if !statIOStamp(t, statIOQuery(t, p, b, c, statIOSelect)).Equal(first) { t.Fatal("cross-family snapshot omitted IO") }
		statIOClear(t, p, b, c)
		if !statIOStamp(t, statIOQuery(t, p, b, c, statIOSelect)).Equal(later) { t.Fatal("cross-family clear") }
		resetReplicationVoid(t, p, b, c, "SELECT pg_stat_reset_shared('io')", "pg_stat_reset_shared")
		if !statIOStamp(t, statIOQuery(t, p, b, c, statIOSelect)).Equal(later) { t.Fatal("reset cleared retained reader") }
		activityExec(t, p, c, "ROLLBACK")
		afterRollback := statIOStamp(t, statIOFresh(t, p, b, c))
		if !afterRollback.After(later) { t.Fatal("rollback undid IO reset", later, afterRollback) }
		// Returned rows retain their old cut while another physical session resets.
		retained, err := c.QueryContext(p.Ctx, statIOSelect)
		if err != nil { t.Fatal(err) }
		newest := statIOResetAfter(t, p, b, peer, "pg_stat_reset_shared('io')", afterRollback)
		if !statIOStamp(t, statIORead(t, retained, b.PG)).Equal(afterRollback) { t.Fatal("materialized result changed after reset") }
		if !statIOStamp(t, statIOFresh(t, p, b, c)).Equal(newest) { t.Fatal("fresh execution reused returned cut") }
		if !b.PG { statIOControls(t, p, b, c, peer, newest) }
	})
}

func statIOControls(t *testing.T, p *fixture.Pair, b fixture.Backend, c, peer *sql.Conn, before time.Time) {
	t.Helper()
	activityExec(t, p, c, "CREATE TABLE io_control_sentinel(v integer)")
	activityExec(t, p, c, "INSERT INTO io_control_sentinel VALUES(37)")
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil { t.Fatal(err) }
	later := statIOResetAfter(t, p, b, peer, "pg_stat_reset_shared('io')", before)
	retained, err := c.QueryContext(p.Ctx, statIOSelect)
	if err != nil { t.Fatal(err) }
	defer retained.Close()
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil { t.Fatal(err) }
	rows := statIOFresh(t, p, b, c)
	afterReset := statIOStamp(t, rows)
	if !afterReset.After(later) { t.Fatal("native reset preserved IO history", afterReset, later) }
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil { t.Fatal(err) }
	rows = statIOFresh(t, p, b, c)
	afterRestore := statIOStamp(t, rows)
	if !afterRestore.After(afterReset) { t.Fatal("dump serialized IO history", afterRestore, afterReset) }
	old := statIORead(t, retained, b.PG)
	statIOCheck(t, old, b.PG)
	if !statIOStamp(t, old).Equal(later) { t.Fatal("native controls mutated retained result") }
	var redump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil { t.Fatal(err) }
	if !bytes.Equal(dump.Bytes(), redump.Bytes()) { t.Fatal("monitoring history changed logical dump") }
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT v FROM io_control_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(37)}}))
}
