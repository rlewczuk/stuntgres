package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func heapStatsKindError(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, name, detail string, exact, approx, pages bool) {
	t.Helper()
	for _, spec := range []struct{ function, typ, code, message string; reject bool }{
		{"pgstattuple", "text", "0A000", "cannot get tuple-level statistics for relation \""+name+"\"", exact},
		{"pgstattuple", "regclass", "0A000", "cannot get tuple-level statistics for relation \""+name+"\"", exact},
		{"pgstattuple_approx", "regclass", "0A000", "relation \""+name+"\" is of wrong relation kind", approx},
		{"pg_relpages", "text", "42809", "cannot get page count of relation \""+name+"\"", pages},
		{"pg_relpages", "regclass", "42809", "cannot get page count of relation \""+name+"\"", pages},
	} {
		if !spec.reject { continue }
		s := pgstatindexError("SELECT * FROM hs_part."+spec.function+"($1::"+spec.typ+")", spec.code, spec.message, "hs_part."+name)
		s.Diagnostic.Detail = "This operation is not supported for "+detail+"."
		heapStatsBoth(t, p, b, c, s)
	}
}

func TestReferencePgstattupleHeapKinds(t *testing.T) {
	t.Run("relation_kinds", func(t *testing.T) {
		heapStatsCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			defer c.ExecContext(p.Ctx, "DROP FOREIGN DATA WRAPPER IF EXISTS hs_heap_fdw CASCADE")
			for _, q := range []string{
				"CREATE VIEW hs_part.heap_view AS SELECT k FROM hs_part.heap",
				"CREATE MATERIALIZED VIEW hs_part.heap_mv AS SELECT k FROM hs_part.heap",
				"CREATE SEQUENCE hs_part.heap_sequence",
				"CREATE TYPE hs_part.heap_composite AS(k integer)",
				"CREATE TABLE hs_part.heap_parent(k integer,p point) PARTITION BY RANGE(k)",
				"CREATE TABLE hs_part.heap_leaf PARTITION OF hs_part.heap_parent DEFAULT",
				"CREATE INDEX heap_parent_index ON hs_part.heap_parent USING gist(p)",
				"CREATE FOREIGN DATA WRAPPER hs_heap_fdw",
				"CREATE SERVER hs_heap_server FOREIGN DATA WRAPPER hs_heap_fdw",
				"CREATE FOREIGN TABLE hs_part.heap_foreign(k integer) SERVER hs_heap_server",
			} { run(fixture.Exec(q, 0)) }
			for _, tc := range []struct{ name, kind string }{{"heap_view", "views"}, {"heap_composite", "composite types"}, {"heap_parent", "partitioned tables"}, {"heap_parent_index", "partitioned indexes"}, {"heap_foreign", "foreign tables"}} {
				heapStatsKindError(t, p, b, c, tc.name, tc.kind, true, true, true)
			}
			for _, name := range []string{"heap_leaf", "heap_mv"} { heapStatsEmpty(t, p, b, c, "hs_part."+name) }
			heapStatsKindError(t, p, b, c, "heap_sequence", "sequences", false, true, false)
			for _, typ := range []string{"text", "regclass"} {
				heapStatsBoth(t, p, b, c, fixture.Select("SELECT tuple_count,dead_tuple_count,table_len=8192 AS valid FROM hs_part.pgstattuple($1::"+typ+")", []string{"tuple_count", "dead_tuple_count", "valid"}, []string{"INT8", "INT8", "BOOL"}, [][]any{{int64(1), int64(0), true}}, "hs_part.heap_sequence"))
				heapStatsBoth(t, p, b, c, fixture.Select("SELECT hs_part.pg_relpages($1::"+typ+") AS pages", []string{"pages"}, []string{"INT8"}, [][]any{{int64(1)}}, "hs_part.heap_sequence"))
				heapStatsBoth(t, p, b, c, fixture.Select("SELECT hs_part.pg_relpages($1::"+typ+") AS pages", []string{"pages"}, []string{"INT8"}, [][]any{{int64(1)}}, "hs_part.heap_index"))
			}
			run(fixture.Exec("INSERT INTO hs_part.heap_parent(k) VALUES(1)", 1))
			heapStatsCounts(t, p, b, c, "hs_part.heap_leaf", 1, 0, 1, 0)
			run(fixture.Exec("INSERT INTO hs_part.heap VALUES(5)", 1))
			run(fixture.Exec("REFRESH MATERIALIZED VIEW hs_part.heap_mv", 0))
			heapStatsCounts(t, p, b, c, "hs_part.heap_mv", 1, 0, 1, 0)
			// Genuine PostgreSQL TOAST/invalid-index construction is oracle-only;
			// native descriptor-level dispatch is a separate production-round proof.
			if b.PG {
				// Native public CREATE INDEX USING gin is unavailable
				// (index/clauses.go unresolvedIndexMethod), not a statistics
				// exception. Production TestPgstattupleHeapCapture must prove
				// native GIN descriptor dispatch, diagnostics and page capture.
				run(fixture.Exec("CREATE TABLE hs_part.gin_heap(v integer[])", 0))
				run(fixture.Exec("CREATE INDEX gin_index ON hs_part.gin_heap USING gin(v)", 0))
				heapStatsKindError(t, p, b, c, "gin_index", "indexes", false, true, false)
				for _, typ := range []string{"text", "regclass"} {
					heapStatsBoth(t, p, b, c, pgstatindexError("SELECT * FROM hs_part.pgstattuple($1::"+typ+")", "0A000", "index \"gin_index\" (gin index) is not supported", "hs_part.gin_index"))
					heapStatsBoth(t, p, b, c, fixture.Select("SELECT hs_part.pg_relpages($1::"+typ+")>0 AS valid", []string{"valid"}, []string{"BOOL"}, [][]any{{true}}, "hs_part.gin_index"))
				}
				run(fixture.Exec("INSERT INTO hs_part.gin_heap VALUES('{1,2}')", 1))
				run(fixture.Exec("CREATE TABLE hs_part.toast_heap(v text)", 0))
				var toast string
				if err := c.QueryRowContext(p.Ctx, "SELECT reltoastrelid::regclass::text FROM pg_class WHERE oid='hs_part.toast_heap'::regclass").Scan(&toast); err != nil { t.Fatal(err) }
				heapStatsEmpty(t, p, b, c, toast)
				run(fixture.Exec("CREATE ACCESS METHOD hs_heap_nonheap TYPE TABLE HANDLER heap_tableam_handler", 0))
				defer c.ExecContext(p.Ctx, "DROP ACCESS METHOD hs_heap_nonheap CASCADE")
				run(fixture.Exec("CREATE TABLE hs_part.nonheap(k integer) USING hs_heap_nonheap", 0))
				for _, spec := range []struct{ name, typ string }{{"pgstattuple", "text"}, {"pgstattuple", "regclass"}, {"pgstattuple_approx", "regclass"}} {
					heapStatsBoth(t, p, b, c, pgstatindexError("SELECT * FROM hs_part."+spec.name+"($1::"+spec.typ+")", "0A000", "only heap AM is supported", "hs_part.nonheap"))
				}
				for _, typ := range []string{"text", "regclass"} {
					heapStatsBoth(t, p, b, c, fixture.Select("SELECT hs_part.pg_relpages($1::"+typ+") AS pages", []string{"pages"}, []string{"INT8"}, [][]any{{int64(0)}}, "hs_part.nonheap"))
				}
				run(fixture.Exec("INSERT INTO hs_part.heap VALUES(1),(1)", 2))
				_, err := c.ExecContext(p.Ctx, "CREATE UNIQUE INDEX CONCURRENTLY heap_invalid ON hs_part.heap(k)")
				fixture.State(t, err, "23505")
				for _, typ := range []string{"text", "regclass"} {
					heapStatsBoth(t, p, b, c, pgstatindexError("SELECT * FROM hs_part.pgstattuple($1::"+typ+")", "55000", "index \"heap_invalid\" is not valid", "hs_part.heap_invalid"))
				}
			}
		})
	})
	for _, tc := range []struct{ method, typ, value string; supported bool }{
		{"btree", "integer", "1", true}, {"hash", "integer", "1", true}, {"gist", "point", "'(1,2)'", true},
		{"spgist", "point", "'(1,2)'", false}, {"brin", "integer", "1", false},
	} {
		t.Run(tc.method, func(t *testing.T) {
			heapStatsCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				for _, q := range []string{"CREATE TABLE hs_part.am_heap(v "+tc.typ+")", "CREATE INDEX am_index ON hs_part.am_heap USING "+tc.method+"(v)"} { fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0)) }
				heapStatsKindError(t, p, b, c, "am_index", "indexes", false, true, false)
				for _, typ := range []string{"text", "regclass"} {
					q := "SELECT * FROM hs_part.pgstattuple($1::"+typ+")"
					if !tc.supported {
						heapStatsBoth(t, p, b, c, pgstatindexError(q, "0A000", "index \"am_index\" ("+tc.method+" index) is not supported", "hs_part.am_index"))
					} else {
						heapStatsBoth(t, p, b, c, fixture.Select("SELECT tuple_count,dead_tuple_count FROM hs_part.pgstattuple($1::"+typ+")", []string{"tuple_count", "dead_tuple_count"}, []string{"INT8", "INT8"}, [][]any{{int64(0), int64(0)}}, "hs_part.am_index"))
					}
					heapStatsBoth(t, p, b, c, fixture.Select("SELECT hs_part.pg_relpages($1::"+typ+")>0 AS valid", []string{"valid"}, []string{"BOOL"}, [][]any{{true}}, "hs_part.am_index"))
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO hs_part.am_heap VALUES("+tc.value+")", 1))
				if tc.supported {
					tuples := int64(1)
					// The release scanner starts at GIST_ROOT_BLKNO+1, so a
					// one-page GiST's root tuple is deliberately not counted.
					if tc.method == "gist" { tuples = 0 }
					heapStatsBoth(t, p, b, c, fixture.Select("SELECT tuple_count,dead_tuple_count FROM hs_part.pgstattuple('hs_part.am_index'::regclass)", []string{"tuple_count", "dead_tuple_count"}, []string{"INT8", "INT8"}, [][]any{{tuples, int64(0)}}))
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM hs_part.am_heap", 1))
					// PostgreSQL keeps the physical item until maintenance. The
					// native simulation counts only current logical membership.
					afterDelete := int64(0)
					if b.PG { afterDelete = tuples }
					heapStatsBoth(t, p, b, c, fixture.Select("SELECT tuple_count,dead_tuple_count FROM hs_part.pgstattuple('hs_part.am_index'::regclass)", []string{"tuple_count", "dead_tuple_count"}, []string{"INT8", "INT8"}, [][]any{{afterDelete, int64(0)}}))
				}
			})
		})
	}
	t.Run("names_oids_and_temp", func(t *testing.T) {
		heapStatsCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			for _, name := range []string{"pgstattuple", "pg_relpages"} {
				for _, arg := range []string{"hs_part.missing", "123", "-"} {
					heapStatsBoth(t, p, b, c, pgstatindexError("SELECT * FROM hs_part."+name+"($1::text)", "42P01", "relation \""+arg+"\" does not exist", arg))
				}
				for _, arg := range []string{"", "\"", "hs_part..heap"} {
					heapStatsBoth(t, p, b, c, pgstatindexError("SELECT * FROM hs_part."+name+"($1::text)", "42602", "invalid name syntax", arg))
				}
			}
			for _, name := range []string{"pgstattuple", "pgstattuple_approx", "pg_relpages"} {
				for _, oid := range []string{"0", "4294967295"} {
					heapStatsBoth(t, p, b, c, pgstatindexError("SELECT * FROM hs_part."+name+"($1::regclass)", "XX000", "could not open relation with OID "+oid, oid))
				}
			}
			peer := heapStatsConn(t, p, b)
			defer peer.Close()
			fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Exec("CREATE TEMP TABLE hs_private(k integer)", 0))
			var temp string
			if err := peer.QueryRowContext(p.Ctx, "SELECT n.nspname FROM pg_namespace n WHERE n.oid=pg_my_temp_schema()").Scan(&temp); err != nil { t.Fatal(err) }
			defer peer.ExecContext(p.Ctx, "DROP TABLE hs_private")
			qualified := fmt.Sprintf("%s.hs_private", temp)
			for _, spec := range []struct{ name, typ string }{{"pgstattuple", "text"}, {"pgstattuple", "regclass"}, {"pgstattuple_approx", "regclass"}} {
				heapStatsBoth(t, p, b, c, pgstatindexError("SELECT * FROM hs_part."+spec.name+"($1::"+spec.typ+")", "0A000", "cannot access temporary tables of other sessions", qualified))
			}
			for _, typ := range []string{"text", "regclass"} {
				heapStatsBoth(t, p, b, c, fixture.Select("SELECT hs_part.pg_relpages($1::"+typ+") AS pages", []string{"pages"}, []string{"INT8"}, [][]any{{int64(0)}}, qualified))
			}
			heapStatsEmpty(t, p, b, c, "hs_part.heap")
		})
	})
}
