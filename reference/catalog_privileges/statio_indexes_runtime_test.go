package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func statioPair(t *testing.T, p *fixture.Pair, c *sql.Conn, name string) [2]int64 {
	t.Helper()
	var pair [2]int64
	if err := c.QueryRowContext(p.Ctx, "SELECT idx_blks_read,idx_blks_hit FROM pg_statio_all_indexes WHERE indexrelname=$1", name).Scan(&pair[0], &pair[1]); err != nil {
		t.Fatal(name, err)
	}
	if pair[0] < 0 || pair[1] < 0 {
		t.Fatal("negative cumulative counters", pair)
	}
	return pair
}

// Every dedicated native physical writer is retired, using the existing public
// drain boundary rather than sleeps, SQL force-flush or private inspection.
func statioTouch(t *testing.T, p *fixture.Pair, b fixture.Backend, table string, settings ...string) {
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
	var got int64
	if err := c.QueryRowContext(p.Ctx, "SELECT k FROM "+table+" WHERE k=1").Scan(&got); err != nil || got != 1 {
		t.Fatal("actual hash result", got, err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if err := pool.Close(); err != nil {
		t.Fatal(err)
	}
}

func statioCounterSequence(t *testing.T, p *fixture.Pair, b fixture.Backend, table string) {
	t.Helper()
	c := vacuumConn(t, p, b)
	statioExec(t, p, b, c, "SET stats_fetch_consistency='none'", "CREATE TABLE "+table+"(k integer)")
	vacuumRun(t, p, b, c, fixture.Exec("INSERT INTO "+table+" VALUES(1),(2)", 2))
	statioExec(t, p, b, c, "CREATE INDEX "+table+"_hash ON "+table+" USING hash(k)", "CREATE INDEX "+table+"_unused ON "+table+"(k)")
	statioNamesFor(t, p, b, c, "relname='"+table+"'", table+"_hash", table+"_unused")
	statioNamesFor(t, p, b, c, "relname='"+table+"' AND schemaname NOT IN ('pg_catalog','information_schema') AND schemaname !~ '^pg_toast'", table+"_hash", table+"_unused")
	statioNamesFor(t, p, b, c, "relname='"+table+"' AND (schemaname IN ('pg_catalog','information_schema') OR schemaname ~ '^pg_toast')")
	if rows := statioRows(t, p, b, c, "SELECT * FROM pg_statio_all_indexes WHERE relname='"+table+"' ORDER BY indexrelname"); len(rows) != 2 {
		t.Fatal("smoke seven-column real rows", rows)
	}
	vacuumRun(t, p, b, c, fixture.Select("SELECT s.indexrelname,s.relid='"+table+"'::regclass AS parent_link,s.relid=x.indrelid AS indexed_parent,s.indexrelid=x.indexrelid AS indexed_id FROM pg_statio_all_indexes s JOIN pg_index x ON x.indexrelid=s.indexrelid WHERE s.relname='"+table+"' ORDER BY s.indexrelname", []string{"indexrelname", "parent_link", "indexed_parent", "indexed_id"}, []string{"NAME", "BOOL", "BOOL", "BOOL"}, [][]any{{table + "_hash", true, true, true}, {table + "_unused", true, true, true}}))
	for i := int64(0); i < 3; i++ {
		statioTouch(t, p, b, table, "SET track_io_timing=off")
		got := statioPair(t, p, c, table+"_hash")
		if !b.PG && got != ([2]int64{1, i}) {
			t.Fatal("deterministic cold/hit sequence", i, got)
		}
		if !b.PG && statioPair(t, p, c, table+"_unused") != ([2]int64{}) {
			t.Fatal("unused index collected")
		}
	}
	before := statioPair(t, p, c, table+"_hash")
	stmt, err := c.PrepareContext(p.Ctx, "SELECT k FROM "+table+" WHERE k=$1")
	if err != nil {
		t.Fatal(err)
	}
	stmt.Close()
	for range 3 {
		if got := statioPair(t, p, c, table+"_hash"); got != before {
			t.Fatal("Prepare/statistics read collected", got, before)
		}
	}
	if b.PG {
		statioExec(t, p, b, c, "SET stats_fetch_consistency='snapshot'", "BEGIN")
		first := statioPair(t, p, c, table+"_hash")
		statioTouch(t, p, b, table)
		if got := statioPair(t, p, c, table+"_hash"); got != first {
			t.Fatal("oracle snapshot changed", got, first)
		}
		var equal bool
		if err := c.QueryRowContext(p.Ctx, "SELECT a.idx_blks_read=u.idx_blks_read AND a.idx_blks_hit=u.idx_blks_hit FROM pg_statio_all_indexes a JOIN pg_statio_user_indexes u ON a.indexrelid=u.indexrelid WHERE a.indexrelname=$1", table+"_hash").Scan(&equal); err != nil || !equal {
			t.Fatal("oracle same-provider siblings", equal, err)
		}
		statioExec(t, p, b, c, "ROLLBACK")
	} else {
		statioExec(t, p, b, c, "CREATE TABLE "+table+"_off(k integer)")
		vacuumRun(t, p, b, c, fixture.Exec("INSERT INTO "+table+"_off VALUES(1)", 1))
		statioExec(t, p, b, c, "CREATE INDEX "+table+"_off_hash ON "+table+"_off USING hash(k)")
		// Native hash candidates are captured only for implicit statements.
		// BEGIN/SET LOCAL would deliberately fall back to a heap scan, so it
		// cannot witness an index cache touch, even with enable_seqscan=off.
		statioTouch(t, p, b, table+"_off", "SET track_counts=off", "SET track_io_timing=on")
		if got := statioPair(t, p, c, table+"_off_hash"); got != ([2]int64{}) {
			t.Fatal("counts off", got)
		}
		statioTouch(t, p, b, table+"_off", "SET track_counts=on", "SET track_io_timing=off")
		if got := statioPair(t, p, c, table+"_off_hash"); got != ([2]int64{0, 1}) {
			t.Fatal("off interval failed to warm cache", got)
		}
	}
	statioExec(t, p, b, c, "DROP INDEX "+table+"_unused")
	statioNamesFor(t, p, b, c, "relname='"+table+"'", table+"_hash")
	statioExec(t, p, b, c, "DROP TABLE "+table)
	statioNamesFor(t, p, b, c, "relname='"+table+"'")
	vacuumRun(t, p, b, c, fixture.ImplicitError("UPDATE pg_statio_all_indexes SET idx_blks_hit=0 WHERE false", fixture.Diagnostic{Code: "55000", Message: "cannot update view \"pg_statio_all_indexes\"", Detail: "Views that do not select from a single table or view are not automatically updatable.", Hint: "To enable updating the view, provide an INSTEAD OF UPDATE trigger or an unconditional ON UPDATE DO INSTEAD rule."}))
}

func TestReferenceStatioAllIndexesCounters(t *testing.T) {
	copyProgressScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		statioCounterSequence(t, p, b, "statio_counters")
	})
	// Independently isolated native drivers must yield the same logical sequence.
	for _, name := range []string{"first", "second"} {
		t.Run(name, func(t *testing.T) {
			p := fixture.NewFixture(t, false, false)
			for _, b := range p.Backends() {
				statioCounterSequence(t, p, b, "statio_repeat")
			}
		})
	}
}

func TestReferenceStatioAllIndexesSmoke(t *testing.T) {
	copyProgressScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) { statioCounterSequence(t, p, b, "statio_smoke") })
}

func TestReferenceStatioAllIndexesLifecycle(t *testing.T) {
	copyProgressScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		owner, peer := vacuumConn(t, p, b), vacuumConn(t, p, b)
		statioExec(t, p, b, owner, "BEGIN", "CREATE TABLE statio_private(k integer)", "CREATE INDEX statio_private_i ON statio_private USING hash(k)")
		vacuumRun(t, p, b, owner, fixture.Exec("INSERT INTO statio_private VALUES(1)", 1))
		statioNamesFor(t, p, b, owner, "relname='statio_private'", "statio_private_i")
		statioNamesFor(t, p, b, peer, "relname='statio_private'")
		statioExec(t, p, b, owner, "SET LOCAL enable_seqscan=off")
		vacuumRun(t, p, b, owner, fixture.Select("SELECT k FROM statio_private WHERE k=1", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}}))
		if !b.PG && statioPair(t, p, owner, "statio_private_i") != ([2]int64{}) {
			t.Fatal("uncommitted object entered shared cache")
		}
		statioExec(t, p, b, owner, "ROLLBACK")
		statioNamesFor(t, p, b, owner, "relname='statio_private'")
		statioExec(t, p, b, owner, "CREATE TABLE statio_life(k integer)", "CREATE INDEX statio_life_i ON statio_life USING hash(k)")
		vacuumRun(t, p, b, owner, fixture.Exec("INSERT INTO statio_life VALUES(1)", 1))
		statioNamesFor(t, p, b, peer, "relname='statio_life'", "statio_life_i")
		statioExec(t, p, b, owner, "BEGIN", "DROP INDEX statio_life_i", "ROLLBACK")
		statioNamesFor(t, p, b, peer, "relname='statio_life'", "statio_life_i")
		statioExec(t, p, b, owner, "BEGIN", "SAVEPOINT statio_save", "CREATE INDEX statio_discard ON statio_life(k)", "ROLLBACK TO statio_save", "COMMIT")
		statioNamesFor(t, p, b, owner, "relname='statio_life'", "statio_life_i")
		statioExec(t, p, b, owner, "CREATE TEMP TABLE statio_temp(k integer)", "CREATE INDEX statio_temp_i ON statio_temp USING hash(k)", "SET enable_seqscan=off")
		vacuumRun(t, p, b, owner, fixture.Exec("INSERT INTO statio_temp VALUES(1)", 1))
		vacuumRun(t, p, b, owner, fixture.Select("SELECT k FROM statio_temp WHERE k=1", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}}))
		statioNamesFor(t, p, b, peer, "relname='statio_temp'", "statio_temp_i")
		if !b.PG && statioPair(t, p, owner, "statio_temp_i") != ([2]int64{}) {
			t.Fatal("TEMP touched shared cache")
		}
		statioExec(t, p, b, owner, "DROP TABLE statio_temp")
		before := statioRows(t, p, b, peer, "SELECT * FROM pg_statio_all_indexes WHERE relname='statio_life'")
		old, err := peer.QueryContext(p.Ctx, "SELECT * FROM pg_statio_all_indexes WHERE relname='statio_life'")
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		metadata := statioMetadata(t, old, b.PG)
		oldID := statioIdentity(t, p, owner, "statio_life_i")
		if b.PG {
			statioExec(t, p, b, owner, "BEGIN", "DROP TABLE statio_life", "COMMIT")
			statioNamesFor(t, p, b, owner, "relname='statio_life'")
			statioExec(t, p, b, owner, "CREATE TABLE statio_life(k integer)", "CREATE INDEX statio_life_i ON statio_life(k)")
			if statioIdentity(t, p, owner, "statio_life_i") == oldID {
				t.Fatal("recreated index identity reused")
			}
		} else {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			statioNamesFor(t, p, b, owner, "relname='statio_life'")
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			statioNamesFor(t, p, b, owner, "relname='statio_life'", "statio_life_i")
			if got := statioPair(t, p, owner, "statio_life_i"); got != ([2]int64{}) {
				t.Fatal("Restore serialized history", got)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewBufferString("CREATE TABLE statio_bad(k missing_type);")); err == nil {
				t.Fatal("bad Restore accepted")
			}
			statioNamesFor(t, p, b, owner, "relname='statio_life'", "statio_life_i")
		}
		var retained [][]any
		for old.Next() {
			values := make([]any, 7)
			args := make([]any, 7)
			for i := range args {
				args[i] = &values[i]
			}
			if err := old.Scan(args...); err != nil {
				t.Fatal(err)
			}
			for i := range values {
				values[i] = fixture.Normalize(values[i])
			}
			retained = append(retained, values)
		}
		if err := old.Err(); err != nil || !reflect.DeepEqual(retained, before) {
			t.Fatal("held values changed", retained, before, err)
		}
		if metadata[0].DatabaseTypeName() != "OID" || metadata[6].DatabaseTypeName() != "INT8" {
			t.Fatal("held metadata changed")
		}
		currentID := statioIdentity(t, p, owner, "statio_life_i")
		pairBeforeCancel := statioPair(t, p, owner, "statio_life_i")
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		// Use database/sql's public pool acquisition boundary. A held pq Conn
		// can finish a fast query before its asynchronous cancel is delivered.
		rows, err := b.DB.QueryContext(ctx, "SELECT k FROM statio_life WHERE k=1")
		if rows != nil {
			rows.Close()
			t.Fatal("precanceled read exposed rows")
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatal("precanceled read", err)
		}
		var state interface{ SQLState() string }
		if errors.As(err, &state) {
			t.Fatal("ordinary cancellation acquired SQLSTATE", state.SQLState())
		}
		if statioIdentity(t, p, owner, "statio_life_i") != currentID {
			t.Fatal("cancellation changed index identity")
		}
		if got := statioPair(t, p, owner, "statio_life_i"); !b.PG && got != pairBeforeCancel {
			t.Fatal("precanceled read collected", got, pairBeforeCancel)
		}
		var sentinel [][]any
		if !b.PG {
			sentinel = [][]any{{int64(1)}}
		}
		vacuumRun(t, p, b, owner, fixture.Select("SELECT k FROM statio_life", []string{"k"}, []string{"INT4"}, sentinel))
	})
}
