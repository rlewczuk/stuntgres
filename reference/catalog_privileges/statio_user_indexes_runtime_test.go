package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func statioUserPair(t *testing.T, p *fixture.Pair, c *sql.Conn, name string) [2]int64 {
	t.Helper()
	var pair [2]int64
	if err := c.QueryRowContext(p.Ctx, "SELECT idx_blks_read,idx_blks_hit FROM pg_statio_user_indexes WHERE indexrelname=$1", name).Scan(&pair[0], &pair[1]); err != nil {
		t.Fatal(name, err)
	}
	if pair[0] < 0 || pair[1] < 0 {
		t.Fatal("negative typed counters", pair)
	}
	return pair
}

// Use the existing dedicated writer-pool retirement convention, while checking
// the public INT4 result metadata as well as the genuine eligible index access.
func statioUserTouch(t *testing.T, p *fixture.Pair, b fixture.Backend, table string, settings ...string) {
	t.Helper()
	pool := fixture.CopyScenarioPool(t, p, b.PG, "", "")
	c, err := pool.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	copyProgressExec(t, p.Ctx, c, "SET enable_seqscan=off")
	for _, q := range settings {
		copyProgressExec(t, p.Ctx, c, q)
	}
	rows, err := c.QueryContext(p.Ctx, "SELECT k FROM "+table+" WHERE k=1")
	if err != nil {
		t.Fatal(err)
	}
	columns, err := rows.ColumnTypes()
	if err != nil {
		rows.Close()
		t.Fatal(err)
	}
	if len(columns) != 1 || columns[0].Name() != "k" || columns[0].DatabaseTypeName() != "INT4" {
		rows.Close()
		t.Fatal("actual hash result metadata", columns)
	}
	var got int64
	if !rows.Next() {
		rows.Close()
		t.Fatal("missing actual hash result", rows.Err())
	}
	if err := rows.Scan(&got); err != nil || got != 1 {
		rows.Close()
		t.Fatal("actual hash result", got, err)
	}
	if rows.Next() {
		rows.Close()
		t.Fatal("unexpected extra hash result")
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if err := pool.Close(); err != nil {
		t.Fatal(err)
	}
}

func statioUserCounterSequence(t *testing.T, p *fixture.Pair, b fixture.Backend, table string) {
	t.Helper()
	c := vacuumConn(t, p, b)
	statioExec(t, p, b, c, "SET stats_fetch_consistency='none'", "CREATE TABLE "+table+"(k integer)")
	vacuumRun(t, p, b, c, fixture.Exec("INSERT INTO "+table+" VALUES(1),(2)", 2))
	statioExec(t, p, b, c, "CREATE INDEX "+table+"_hash ON "+table+" USING hash(k)", "CREATE INDEX "+table+"_unused ON "+table+"(k)")
	predicate := "relname='" + table + "'"
	statioUserNames(t, p, b, c, predicate, table+"_hash", table+"_unused")
	for _, view := range []string{"pg_statio_user_indexes", "pg_catalog.pg_statio_user_indexes"} {
		q := "SELECT * FROM " + view + " WHERE " + predicate + " ORDER BY indexrelname"
		if got := statioRows(t, p, b, c, q); len(got) != 2 {
			t.Fatal("new named view", got)
		}
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		rows, err := stmt.QueryContext(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		if got := statioConsumeRows(t, rows, b.PG); len(got) != 2 {
			t.Fatal("prepared new view", got)
		}
		stmt.Close()
	}
	s := fixture.Select("SELECT s.indexrelname,s.relid='"+table+"'::regclass AS parent_link,s.relid=x.indrelid AS indexed_parent,s.indexrelid=x.indexrelid AS indexed_id FROM pg_statio_user_indexes s JOIN pg_index x ON x.indexrelid=s.indexrelid WHERE "+predicate+" ORDER BY indexrelname", []string{"indexrelname", "parent_link", "indexed_parent", "indexed_id"}, []string{"NAME", "BOOL", "BOOL", "BOOL"}, [][]any{{table + "_hash", true, true, true}, {table + "_unused", true, true, true}})
	vacuumRun(t, p, b, c, s)
	vacuumRun(t, p, b, c, fixture.Prepared(s))
	statioNamesFor(t, p, b, c, predicate+" AND (schemaname IN ('pg_catalog','information_schema') OR schemaname ~ '^pg_toast')")
	if b.PG {
		vacuumRun(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM pg_statio_sys_indexes WHERE "+predicate, []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
	} // Native named SYS counterpart is explicitly scheduler-staged.
	if !b.PG && statioUserPair(t, p, c, table+"_hash") != ([2]int64{}) {
		t.Fatal("DDL manufactured access")
	}
	for hit := int64(0); hit < 3; hit++ {
		// An actual eligible autocommit hash lookup, with no sleep or SQL flush.
		statioUserTouch(t, p, b, table, "SET track_io_timing=off")
		got := statioUserPair(t, p, c, table+"_hash")
		if !b.PG && got != ([2]int64{1, hit}) {
			t.Fatal("native cold/hit sequence", hit, got)
		}
		if !b.PG && statioUserPair(t, p, c, table+"_unused") != ([2]int64{}) {
			t.Fatal("unused index counted")
		}
		if rows := statioUserShared(t, p, b, c, predicate); len(rows) != 2 {
			t.Fatal("shared pairs", rows)
		}
	}
	before := statioUserPair(t, p, c, table+"_hash")
	stmt, err := c.PrepareContext(p.Ctx, "SELECT k FROM "+table+" WHERE k=$1")
	if err != nil {
		t.Fatal(err)
	}
	stmt.Close()
	for range 3 {
		if got := statioUserPair(t, p, c, table+"_hash"); got != before {
			t.Fatal("prepare/describe/stat read collected", got, before)
		}
	}
	if !b.PG {
		statioExec(t, p, b, c, "CREATE TABLE "+table+"_off(k integer)")
		vacuumRun(t, p, b, c, fixture.Exec("INSERT INTO "+table+"_off VALUES(1)", 1))
		statioExec(t, p, b, c, "CREATE INDEX "+table+"_off_hash ON "+table+"_off USING hash(k)")
		statioUserTouch(t, p, b, table+"_off", "SET track_counts=off", "SET track_io_timing=on")
		if got := statioUserPair(t, p, c, table+"_off_hash"); got != ([2]int64{}) {
			t.Fatal("counts-off collected", got)
		}
		statioUserTouch(t, p, b, table+"_off", "SET track_counts=on", "SET track_io_timing=off")
		if got := statioUserPair(t, p, c, table+"_off_hash"); got != ([2]int64{0, 1}) {
			t.Fatal("counts-off failed to warm cache", got)
		}
		statioUserShared(t, p, b, c, "relname='"+table+"_off'")
	}
	vacuumRun(t, p, b, c, fixture.ImplicitError("UPDATE pg_statio_user_indexes SET idx_blks_hit=0 WHERE false", fixture.Diagnostic{Code: "55000", Message: "cannot update view \"pg_statio_all_indexes\"", Detail: "Views that do not select from a single table or view are not automatically updatable.", Hint: "To enable updating the view, provide an INSTEAD OF UPDATE trigger or an unconditional ON UPDATE DO INSTEAD rule."}))
	held, err := c.PrepareContext(p.Ctx, "SELECT * FROM pg_statio_user_indexes WHERE "+predicate+" ORDER BY indexrelname")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	statioExec(t, p, b, c, "DROP INDEX "+table+"_unused")
	statioUserNames(t, p, b, c, predicate, table+"_hash")
	statioNamesFor(t, p, b, c, predicate, table+"_hash")
	statioExec(t, p, b, c, "DROP TABLE "+table)
	statioUserNames(t, p, b, c, predicate)
	statioNamesFor(t, p, b, c, predicate)
	rows, err := held.QueryContext(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := statioConsumeRows(t, rows, b.PG); len(got) != 0 {
		t.Fatal("held prepared did not recapture drop", got)
	}
}

func TestReferenceStatioUserIndexesCounters(t *testing.T) {
	copyProgressScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		statioUserCounterSequence(t, p, b, "statio_user_counters")
	})
	for _, name := range []string{"isolated_first", "isolated_second"} {
		t.Run(name, func(t *testing.T) {
			p := fixture.NewFixture(t, false, false)
			for _, b := range p.Backends() {
				statioUserCounterSequence(t, p, b, "statio_user_repeat")
			}
		})
	}
}

func TestReferenceStatioUserIndexesSmoke(t *testing.T) {
	copyProgressScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		statioUserCounterSequence(t, p, b, "statio_user_smoke")
	})
}
