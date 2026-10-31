package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePgstattupleHeapControls(t *testing.T) {
	heapStatsCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, q := range []string{
			"CREATE SCHEMA hs_moved",
			"CREATE VIEW hs_part.oid_stats AS SELECT * FROM hs_part.pgstattuple('hs_part.heap'::regclass)",
			"CREATE VIEW hs_part.text_stats AS SELECT * FROM hs_part.pgstattuple('hs_part.heap'::text)",
			"CREATE VIEW hs_part.approx_stats AS SELECT * FROM hs_part.pgstattuple_approx('hs_part.heap'::regclass)",
			"CREATE VIEW hs_part.page_stats AS SELECT hs_part.pg_relpages('hs_part.heap'::regclass) AS pages",
			"CREATE TABLE hs_part.consumer(v bigint DEFAULT hs_part.pg_relpages('hs_part.heap'::regclass),CHECK(v>=0 AND hs_part.pg_relpages('hs_part.heap'::regclass)>=0))",
		} { run(fixture.Exec(q, 0)) }
		run(fixture.Exec("INSERT INTO hs_part.heap VALUES(1)", 1))
		for _, q := range []string{
			"ALTER TABLE hs_part.heap RENAME TO kept_heap",
			"CREATE TABLE hs_part.heap(k integer)",
		} { run(fixture.Exec(q, 0)) }
		run(fixture.Exec("INSERT INTO hs_part.heap VALUES(7),(8)", 2))
		// Constant regclass references retain identity; stored text resolves the
		// replacement name. The same expressions survive member moves/relocation.
		for _, q := range []string{
			"ALTER TABLE hs_part.kept_heap SET SCHEMA hs_moved",
			"ALTER FUNCTION hs_part.pgstattuple(text) RENAME TO heap_exact_text",
			"ALTER FUNCTION hs_part.heap_exact_text(text) SET SCHEMA hs_moved",
		} { run(fixture.Exec(q, 0)) }
		post := func(target *sql.Conn, rec *fixture.Recorder) {
			for _, s := range []fixture.Step{
				fixture.Select("SELECT tuple_count,dead_tuple_count FROM hs_part.oid_stats", []string{"tuple_count", "dead_tuple_count"}, []string{"INT8", "INT8"}, [][]any{{int64(1), int64(0)}}),
				fixture.Select("SELECT tuple_count,dead_tuple_count FROM hs_part.text_stats", []string{"tuple_count", "dead_tuple_count"}, []string{"INT8", "INT8"}, [][]any{{int64(2), int64(0)}}),
				fixture.Select("SELECT approx_tuple_count,dead_tuple_count FROM hs_part.approx_stats", []string{"approx_tuple_count", "dead_tuple_count"}, []string{"INT8", "INT8"}, [][]any{{int64(1), int64(0)}}),
				fixture.Select("SELECT pages FROM hs_part.page_stats", []string{"pages"}, []string{"INT8"}, [][]any{{int64(1)}}),
			} {
				fixture.RunStep(t, p.Ctx, target, rec, s)
				fixture.RunStep(t, p.Ctx, target, rec, fixture.Prepared(s))
			}
		}
		post(c, b.Recorder)
		run(fixture.Exec("ALTER FUNCTION hs_moved.heap_exact_text(text) SET SCHEMA hs_part", 0))
		run(fixture.Exec("ALTER EXTENSION pgstattuple SET SCHEMA hs_moved", 0))
		post(c, b.Recorder)
		run(fixture.Exec("INSERT INTO hs_part.consumer DEFAULT VALUES", 1))
		run(fixture.Select("SELECT v FROM hs_part.consumer", []string{"v"}, []string{"INT8"}, [][]any{{int64(1)}}))
		q := "SELECT tuple_count,dead_tuple_count FROM hs_part.oid_stats"
		st, err := c.PrepareContext(p.Ctx, q)
		if err != nil { t.Fatal(err) }
		defer st.Close()
		held := fixture.HeldStatement{Executor: c, Statement: st, SQL: q}
		readHeld := func() { fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"tuple_count", "dead_tuple_count"}, []string{"INT8", "INT8"}, [][]any{{int64(1), int64(0)}})) }
		readHeld()
		if b.PG { return }
		peer := heapStatsConn(t, p, b)
		defer peer.Close()
		fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Exec("BEGIN", 0))
		defer peer.ExecContext(p.Ctx, "ROLLBACK")
		fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Exec("INSERT INTO hs_moved.kept_heap VALUES(99)", 1))
		dump := fixture.ExtensionDump(t, p.Ctx, p.Native)
		fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Exec("ROLLBACK", 0))
		old, err := c.QueryContext(p.Ctx, q)
		if err != nil { t.Fatal(err) }
		defer old.Close()
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil { t.Fatal(err) }
		if !old.Next() { t.Fatal("missing retained row", old.Err()) }
		var live, dead int64
		if err := old.Scan(&live, &dead); err != nil || live != 1 || dead != 0 { t.Fatal("retained stats", live, dead, err) }
		if old.Next() || old.Err() != nil { t.Fatal("retained tail", old.Err()) }
		old.Close()
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump)); err != nil { t.Fatal(err) }
		post(c, b.Recorder)
		readHeld()
		run(fixture.Select("SELECT k FROM hs_moved.kept_heap", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}}))
		before := fixture.ExtensionDump(t, p.Ctx, p.Native)
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump+"\nINVALID SQL;")); err == nil { t.Fatal("invalid restore accepted") }
		if fixture.ExtensionDump(t, p.Ctx, p.Native) != before { t.Fatal("rejected restore changed catalog or heap") }
		ctx, cancel := context.WithCancel(p.Ctx)
		err = stuntgres.Restore(ctx, p.Native, &fixture.CatalogControlCancelReader{Cancel: cancel})
		cancel()
		if !errors.Is(err, context.Canceled) { t.Fatal("canceled restore", err) }
		if fixture.ExtensionDump(t, p.Ctx, p.Native) != before { t.Fatal("canceled restore changed catalog or heap") }
		post(c, b.Recorder)
		readHeld()
		other, dest, rec := pgcryptoControlDestination(t, p, "hs_heap_destination")
		fixture.RunStep(t, p.Ctx, dest, rec, fixture.Exec("CREATE TABLE destination_collision(k integer)", 0))
		fixture.RunStep(t, p.Ctx, dest, rec, fixture.Exec("CREATE TEMP TABLE hs_session_keep(k integer)", 0))
		fixture.RunStep(t, p.Ctx, dest, rec, fixture.Exec("INSERT INTO hs_session_keep VALUES(19)", 1))
		if err := stuntgres.Restore(p.Ctx, other, strings.NewReader(dump)); err != nil { t.Fatal(err) }
		post(dest, rec)
		fixture.RunStep(t, p.Ctx, dest, rec, fixture.Select("SELECT k FROM hs_session_keep", []string{"k"}, []string{"INT4"}, [][]any{{int64(19)}}))
		fixture.RunStep(t, p.Ctx, dest, rec, fixture.Exec("INSERT INTO hs_moved.kept_heap VALUES(20)", 1))
		fixture.RunStep(t, p.Ctx, dest, rec, fixture.Select("SELECT tuple_count FROM hs_part.oid_stats", []string{"tuple_count"}, []string{"INT8"}, [][]any{{int64(2)}}))
		post(c, b.Recorder)
	})
}

func TestReferencePgstattupleHeapMaintenance(t *testing.T) {
	heapStatsCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		horizon := heapStatsHorizon(t, p, b)
		defer horizon.Close()
		defer horizon.ExecContext(p.Ctx, "ROLLBACK")
		run(fixture.Exec("INSERT INTO hs_part.heap VALUES(1),(2)", 2))
		run(fixture.Exec("UPDATE hs_part.heap SET k=3 WHERE k=1", 1))
		run(fixture.Exec("DELETE FROM hs_part.heap WHERE k=2", 1))
		heapStatsCounts(t, p, b, c, "hs_part.heap", 1, 2, 1, 2)
		run(fixture.Exec("VACUUM hs_part.heap", 0))
		// A retained old horizon prevents retirement of genuine predecessors.
		heapStatsCounts(t, p, b, c, "hs_part.heap", 1, 2, 1, 2)
		fixture.RunStep(t, p.Ctx, horizon, b.Recorder, fixture.Exec("ROLLBACK", 0))
		run(fixture.Exec("VACUUM hs_part.heap", 0))
		heapStatsCounts(t, p, b, c, "hs_part.heap", 1, 0, 1, 0)
		q := "SELECT (hs_part.pgstattuple($1::regclass)).tuple_count AS live"
		st, err := c.PrepareContext(p.Ctx, q)
		if err != nil { t.Fatal(err) }
		defer st.Close()
		held := fixture.HeldStatement{Executor: c, Statement: st, SQL: q}
		run(fixture.Exec("TRUNCATE hs_part.heap", 0))
		heapStatsEmpty(t, p, b, c, "hs_part.heap")
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"live"}, []string{"INT8"}, [][]any{{int64(0)}}, "hs_part.heap"))
		run(fixture.Exec("INSERT INTO hs_part.heap VALUES(7)", 1))
		heapStatsCounts(t, p, b, c, "hs_part.heap", 1, 0, 1, 0)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"live"}, []string{"INT8"}, [][]any{{int64(1)}}, "hs_part.heap"))
		run(fixture.Exec("ALTER TABLE hs_part.heap ADD COLUMN payload text DEFAULT 'kept'", 0))
		heapStatsCounts(t, p, b, c, "hs_part.heap", 1, 0, 1, 0)
		run(fixture.Select("SELECT k,payload FROM hs_part.heap", []string{"k", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "kept"}}))
	})
}
