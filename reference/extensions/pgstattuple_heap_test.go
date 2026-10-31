package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgstattupleHeapDeclarations(t *testing.T) {
	heapStatsCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		q := "SELECT p.proname::text AS name,p.pronargs::integer AS nargs,p.proargtypes::text AS args,p.prorettype::bigint AS result,p.proallargtypes::text AS allargs,p.proargmodes::text AS modes,p.proargnames::text AS names,p.proretset AS sets,p.proisstrict AS strict,p.provolatile::text AS volatility,p.proparallel::text AS parallel,p.prosrc AS source,p.probin AS binary,l.lanname::text AS language FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_language l ON l.oid=p.prolang WHERE n.nspname='hs_part' AND p.proname IN ('pgstattuple','pgstattuple_approx','pg_relpages','pgstatindex') ORDER BY p.proname,p.proargtypes::text"
		heapStatsBoth(t, p, b, c, fixture.Select(q,
			[]string{"name", "nargs", "args", "result", "allargs", "modes", "names", "sets", "strict", "volatility", "parallel", "source", "binary", "language"},
			[]string{"TEXT", "INT4", "TEXT", "INT8", "TEXT", "TEXT", "TEXT", "BOOL", "BOOL", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT"},
			[][]any{
				{"pg_relpages", int64(1), "2205", int64(20), nil, nil, "{relname}", false, true, "v", "s", "pg_relpagesbyid_v1_5", "$libdir/pgstattuple", "c"},
				{"pg_relpages", int64(1), "25", int64(20), nil, nil, "{relname}", false, true, "v", "s", "pg_relpages_v1_5", "$libdir/pgstattuple", "c"},
				{"pgstatindex", int64(1), "2205", int64(2249), "{2205,23,23,20,20,20,20,20,20,701,701}", "{i,o,o,o,o,o,o,o,o,o,o}", "{relname,version,tree_level,index_size,root_block_no,internal_pages,leaf_pages,empty_pages,deleted_pages,avg_leaf_density,leaf_fragmentation}", false, true, "v", "s", "pgstatindexbyid_v1_5", "$libdir/pgstattuple", "c"},
				{"pgstatindex", int64(1), "25", int64(2249), "{25,23,23,20,20,20,20,20,20,701,701}", "{i,o,o,o,o,o,o,o,o,o,o}", "{relname,version,tree_level,index_size,root_block_no,internal_pages,leaf_pages,empty_pages,deleted_pages,avg_leaf_density,leaf_fragmentation}", false, true, "v", "s", "pgstatindex_v1_5", "$libdir/pgstattuple", "c"},
				{"pgstattuple", int64(1), "2205", int64(2249), "{2205,20,20,20,701,20,20,701,20,701}", "{i,o,o,o,o,o,o,o,o,o}", "{reloid,table_len,tuple_count,tuple_len,tuple_percent,dead_tuple_count,dead_tuple_len,dead_tuple_percent,free_space,free_percent}", false, true, "v", "s", "pgstattuplebyid_v1_5", "$libdir/pgstattuple", "c"},
				{"pgstattuple", int64(1), "25", int64(2249), "{25,20,20,20,701,20,20,701,20,701}", "{i,o,o,o,o,o,o,o,o,o}", "{relname,table_len,tuple_count,tuple_len,tuple_percent,dead_tuple_count,dead_tuple_len,dead_tuple_percent,free_space,free_percent}", false, true, "v", "s", "pgstattuple_v1_5", "$libdir/pgstattuple", "c"},
				{"pgstattuple_approx", int64(1), "2205", int64(2249), "{2205,20,701,20,20,701,20,20,701,20,701}", "{i,o,o,o,o,o,o,o,o,o,o}", "{reloid,table_len,scanned_percent,approx_tuple_count,approx_tuple_len,approx_tuple_percent,dead_tuple_count,dead_tuple_len,dead_tuple_percent,approx_free_space,approx_free_percent}", false, true, "v", "s", "pgstattuple_approx_v1_5", "$libdir/pgstattuple", "c"},
			}))
		heapStatsEmpty(t, p, b, c, "hs_part.heap")
		for _, typ := range []string{"text", "regclass"} {
			heapStatsBoth(t, p, b, c, fixture.Select("SELECT * FROM hs_part.pgstattuple($1::"+typ+")", heapStatsNames, heapStatsTypes, [][]any{make([]any, 9)}, nil))
			heapStatsBoth(t, p, b, c, fixture.Select("SELECT * FROM hs_part.pg_relpages($1::"+typ+")", []string{"pg_relpages"}, []string{"INT8"}, [][]any{{nil}}, nil))
			heapStatsBoth(t, p, b, c, fixture.Select("SELECT hs_part.pgstattuple($1::"+typ+") IS NULL AS tuple_null,hs_part.pg_relpages($1::"+typ+") IS NULL AS pages_null", []string{"tuple_null", "pages_null"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}, nil))
			heapStatsBoth(t, p, b, c, fixture.Select("SELECT hs_part.pgstattuple($1::"+typ+") AS s", []string{"s"}, []string{"RECORD"}, [][]any{{nil}}, nil))
			heapStatsBoth(t, p, b, c, fixture.Select("SELECT hs_part.pg_relpages($1::"+typ+") AS s", []string{"s"}, []string{"INT8"}, [][]any{{nil}}, nil))
		}
		heapStatsBoth(t, p, b, c, fixture.Select("SELECT * FROM hs_part.pgstattuple_approx($1::regclass)", heapApproxNames, heapApproxTypes, [][]any{make([]any, 10)}, nil))
		heapStatsBoth(t, p, b, c, fixture.Select("SELECT hs_part.pgstattuple_approx($1::regclass) AS s", []string{"s"}, []string{"RECORD"}, [][]any{{nil}}, nil))
		for _, call := range []string{"pgstattuple(relname => 'hs_part.heap'::text)", "pgstattuple(reloid => 'hs_part.heap'::regclass)", "pgstattuple_approx(reloid => 'hs_part.heap'::regclass)"} {
			column := "tuple_count"
			if call == "pgstattuple_approx(reloid => 'hs_part.heap'::regclass)" { column = "approx_tuple_count" }
			heapStatsBoth(t, p, b, c, fixture.Select("SELECT (hs_part."+call+")."+column+" AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		}
		for _, typ := range []string{"text", "regclass"} {
			heapStatsBoth(t, p, b, c, fixture.Select("SELECT hs_part.pg_relpages(relname => $1::"+typ+") AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}, "hs_part.heap"))
		}
		// A held statement must rebind input rather than retain the first target.
		q = "SELECT (hs_part.pgstattuple($1::regclass)).tuple_count AS n"
		st, err := c.PrepareContext(p.Ctx, q)
		if err != nil { t.Fatal(err) }
		defer st.Close()
		held := fixture.HeldStatement{Executor: c, Statement: st, SQL: q}
		for _, s := range []fixture.Step{
			fixture.Select(q, []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}, "hs_part.heap"),
			fixture.Select(q, []string{"n"}, []string{"INT8"}, [][]any{{nil}}, nil),
			fixture.Select(q, []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}, "hs_part.heap"),
		} { fixture.RunStep(t, p.Ctx, held, b.Recorder, s) }
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE SEQUENCE hs_part.eval_once", 0))
		for _, spec := range []struct{ call, column string }{
			{"pgstattuple(CASE WHEN nextval('hs_part.eval_once')>0 THEN 'hs_part.heap'::text ELSE NULL END)", "tuple_count"},
			{"pgstattuple_approx(CASE WHEN nextval('hs_part.eval_once')>0 THEN 'hs_part.heap'::regclass ELSE NULL END)", "approx_tuple_count"},
			{"pg_relpages(CASE WHEN nextval('hs_part.eval_once')>0 THEN 'hs_part.heap'::text ELSE NULL END)", "pg_relpages"},
		} {
			heapStatsBoth(t, p, b, c, fixture.Select("SELECT "+spec.column+" AS n FROM hs_part."+spec.call, []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		}
		run(fixture.Select("SELECT currval('hs_part.eval_once') AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(6)}}))
		run(fixture.Exec("CREATE TABLE hs_part.inputs(id integer,n text)", 0))
		run(fixture.Exec("INSERT INTO hs_part.inputs VALUES(1,'hs_part.heap'),(2,NULL)", 2))
		heapStatsBoth(t, p, b, c, fixture.Select("SELECT i.id,s.tuple_count FROM hs_part.inputs i CROSS JOIN LATERAL hs_part.pgstattuple(i.n) s ORDER BY i.id", []string{"id", "tuple_count"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(0)}, {int64(2), nil}}))
		// text has an implicit cast to regclass. Successful coercion does not
		// mean the extension declares an additional text/named overload.
		for _, call := range []string{
			"pgstattuple(reloid => 'hs_part.heap'::text)",
			"pgstattuple_approx('hs_part.heap'::text)",
		} {
			column := "tuple_count"
			if call == "pgstattuple_approx('hs_part.heap'::text)" { column = "approx_tuple_count" }
			heapStatsBoth(t, p, b, c, fixture.Select("SELECT "+column+" AS n FROM hs_part."+call, []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		}
		for _, tc := range []struct{ call, signature string }{
			{"pgstattuple()", "hs_part.pgstattuple()"},
			{"pgstattuple(true)", "hs_part.pgstattuple(boolean)"},
			{"pgstattuple(missing => 'hs_part.heap'::text)", "hs_part.pgstattuple(missing => text)"},
			{"pgstattuple_approx(relname => 'hs_part.heap'::regclass)", "hs_part.pgstattuple_approx(relname => regclass)"},
			{"pg_relpages(true)", "hs_part.pg_relpages(boolean)"},
		} {
			query := "SELECT * FROM hs_part."+tc.call
			d := fixture.ControlsReferenceDiagnostic("42883", "function "+tc.signature+" does not exist")
			d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
			d.Position = "15"
			s := fixture.ImplicitError(query, d)
			s.SelectRows = true
			run(s)
			failed, err := c.PrepareContext(p.Ctx, query)
			if failed != nil { failed.Close() }
			fixture.AssertDiagnostic(t, err, &d)
		}
		if err := st.Close(); err != nil { t.Fatal(err) }
		run(fixture.Exec("DROP EXTENSION pgstattuple CASCADE", 0))
		for _, name := range []string{"pgstattuple", "pgstattuple_approx", "pg_relpages"} {
			query := "SELECT * FROM hs_part."+name+"(NULL::regclass)"
			d := fixture.ControlsReferenceDiagnostic("42883", "function hs_part."+name+"(regclass) does not exist")
			d.Position = "15"
			d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
			s := fixture.ImplicitError(query, d)
			s.SelectRows = true
			run(s)
			failed, err := c.PrepareContext(p.Ctx, query)
			if failed != nil { failed.Close() }
			fixture.AssertDiagnostic(t, err, &d)
		}
	})
}

func TestReferencePgstattupleHeapCounts(t *testing.T) {
	heapStatsCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		horizon := heapStatsHorizon(t, p, b)
		defer horizon.Close()
		defer horizon.ExecContext(p.Ctx, "ROLLBACK")
		heapStatsEmpty(t, p, b, c, "hs_part.heap")
		run(fixture.Exec("INSERT INTO hs_part.heap VALUES(1),(2),(3)", 3))
		heapStatsCounts(t, p, b, c, "hs_part.heap", 3, 0, 3, 0)
		run(fixture.Exec("DELETE FROM hs_part.heap WHERE k=3", 1))
		heapStatsCounts(t, p, b, c, "hs_part.heap", 2, 1, 2, 1)
		run(fixture.Exec("UPDATE hs_part.heap SET k=10 WHERE k=1", 1))
		heapStatsCounts(t, p, b, c, "hs_part.heap", 2, 2, 2, 2)
		run(fixture.Exec("UPDATE hs_part.heap SET k=11 WHERE k=10", 1))
		heapStatsCounts(t, p, b, c, "hs_part.heap", 2, 3, 2, 3)
		// An old SELECT snapshot stays empty, but statistics inspect current tuples.
		fixture.RunStep(t, p.Ctx, horizon, b.Recorder, fixture.Select("SELECT k FROM hs_part.heap", []string{"k"}, []string{"INT4"}, nil))
		heapStatsCounts(t, p, b, horizon, "hs_part.heap", 2, 3, 2, 3)
		for _, q := range []string{"CREATE TABLE hs_part.\"Quoted Heap\"(k integer)", "CREATE TABLE hs_part.\"123\"(k integer)", "CREATE TABLE hs_part.\"-\"(k integer)"} { run(fixture.Exec(q, 0)) }
		for _, name := range []string{"hs_part.\"Quoted Heap\"", "hs_part.\"123\"", "hs_part.\"-\""} { heapStatsEmpty(t, p, b, c, name) }
		run(fixture.Exec("SET search_path=hs_part,pg_catalog", 0))
		defer c.ExecContext(p.Ctx, "RESET search_path")
		for _, name := range []string{"123", "-"} {
			heapStatsBoth(t, p, b, c, fixture.Select("SELECT (hs_part.pgstattuple($1::text)).tuple_count AS n,hs_part.pg_relpages($1::text) AS pages", []string{"n", "pages"}, []string{"INT8", "INT8"}, [][]any{{int64(0), int64(0)}}, name))
		}
	})
}
