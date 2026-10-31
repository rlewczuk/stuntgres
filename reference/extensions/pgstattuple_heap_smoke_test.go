package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePgstattupleHeapFeatureSmoke(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"hs_smoke_reader"})
	heapStatsCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, writer *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, writer, b.Recorder, s) }
		peer := heapStatsConn(t, p, b)
		defer peer.Close()
		defer writer.ExecContext(p.Ctx, "ROLLBACK")
		defer writer.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		heapStatsEmpty(t, p, b, writer, "hs_part.heap")
		pgstatindexEmpty(t, p, writer, "SELECT * FROM hs_part.pgstatindex('hs_part.heap_index'::regclass)")
		run(fixture.Exec("INSERT INTO hs_part.heap VALUES(1)", 1))
		heapStatsCounts(t, p, b, writer, "hs_part.heap", 1, 0, 1, 0)
		q := "SELECT (hs_part.pgstattuple($1::regclass)).tuple_count AS live"
		st, err := peer.PrepareContext(p.Ctx, q)
		if err != nil { t.Fatal(err) }
		defer st.Close()
		held := fixture.HeldStatement{Executor: peer, Statement: st, SQL: q}
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("INSERT INTO hs_part.heap VALUES(2)", 1))
		heapStatsCounts(t, p, b, peer, "hs_part.heap", 2, 0, 1, 1)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"live"}, []string{"INT8"}, [][]any{{int64(2)}}, "hs_part.heap"))
		fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select("SELECT k FROM hs_part.heap", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}}))
		run(fixture.Exec("ROLLBACK", 0))
		heapStatsLive(t, p, b, peer, 1)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"live"}, []string{"INT8"}, [][]any{{int64(1)}}, "hs_part.heap"))
		// Start a fresh physical incarnation after the aborted insert; genuine
		// PostgreSQL aborted debris is not a portable dead-count expectation.
		run(fixture.Exec("TRUNCATE hs_part.heap", 0))
		horizon := heapStatsHorizon(t, p, b)
		defer horizon.Close()
		defer horizon.ExecContext(p.Ctx, "ROLLBACK")
		run(fixture.Exec("INSERT INTO hs_part.heap VALUES(1)", 1))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("UPDATE hs_part.heap SET k=3", 1))
		heapStatsCounts(t, p, b, writer, "hs_part.heap", 1, 1, 1, 1)
		heapStatsCounts(t, p, b, peer, "hs_part.heap", 2, 0, 1, 1)
		run(fixture.Exec("COMMIT", 0))
		heapStatsCounts(t, p, b, peer, "hs_part.heap", 1, 1, 1, 1)
		heapStatsBoth(t, p, b, writer, fixture.Select("SELECT * FROM hs_part.pgstattuple(NULL::text)", heapStatsNames, heapStatsTypes, [][]any{make([]any, 9)}))
		for _, sql := range []string{"CREATE ROLE hs_smoke_reader", "GRANT USAGE ON SCHEMA hs_part TO hs_smoke_reader", "GRANT EXECUTE ON FUNCTION hs_part.pgstattuple(text) TO hs_smoke_reader", "SET SESSION AUTHORIZATION hs_smoke_reader"} { run(fixture.Exec(sql, 0)) }
		heapStatsBoth(t, p, b, writer, pgstatindexError("SELECT k FROM hs_part.heap", "42501", "permission denied for table heap"))
		heapStatsBoth(t, p, b, writer, fixture.Select("SELECT tuple_count,dead_tuple_count FROM hs_part.pgstattuple($1::text)", []string{"tuple_count", "dead_tuple_count"}, []string{"INT8", "INT8"}, [][]any{{int64(1), int64(1)}}, "hs_part.heap"))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION hs_part.pgstattuple(text) FROM hs_smoke_reader", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION hs_smoke_reader", 0))
		heapStatsBoth(t, p, b, writer, pgstatindexError("SELECT * FROM hs_part.pgstattuple($1::text)", "42501", "permission denied for function pgstattuple", nil))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("CREATE VIEW hs_part.smoke_stats AS SELECT * FROM hs_part.pgstattuple('hs_part.heap'::regclass)", 0))
		fixture.RunStep(t, p.Ctx, horizon, b.Recorder, fixture.Exec("ROLLBACK", 0))
		if b.PG { return }
		old, err := writer.QueryContext(p.Ctx, "SELECT tuple_count,dead_tuple_count FROM hs_part.smoke_stats")
		if err != nil { t.Fatal(err) }
		defer old.Close()
		dump := fixture.ExtensionDump(t, p.Ctx, p.Native)
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil { t.Fatal(err) }
		if !old.Next() { t.Fatal("missing retained smoke row", old.Err()) }
		var live, dead int64
		if err := old.Scan(&live, &dead); err != nil || live != 1 || dead != 1 { t.Fatal("retained smoke row", live, dead, err) }
		if old.Next() || old.Err() != nil { t.Fatal("retained smoke tail", old.Err()) }
		old.Close()
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump)); err != nil { t.Fatal(err) }
		heapStatsCounts(t, p, b, writer, "hs_part.heap", 1, 0, 1, 0)
		run(fixture.Select("SELECT tuple_count,dead_tuple_count FROM hs_part.smoke_stats", []string{"tuple_count", "dead_tuple_count"}, []string{"INT8", "INT8"}, [][]any{{int64(1), int64(0)}}))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"live"}, []string{"INT8"}, [][]any{{int64(1)}}, "hs_part.heap"))
	})
}
