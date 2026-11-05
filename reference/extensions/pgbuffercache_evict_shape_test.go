package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgBuffercacheEvictionSignatures(t *testing.T) {
	bcScenario(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		t.Run("released_catalog", func(t *testing.T) {
			// REL_18_6 pg_buffercache--1.5--1.6.sql: unnamed INs,
			// declared OUTs, non-SETOF records. Filter selected names only:
			// PostgreSQL's full module has additional out-of-scope members.
			q := "SELECT p.proname::text AS name,p.pronargs::integer AS nargs,p.proargtypes::text AS args,p.prorettype::bigint AS result,p.proallargtypes::text AS allargs,p.proargmodes::text AS modes,p.proargnames::text AS names,p.proretset AS sets,p.proisstrict AS strict,p.provolatile::text AS volatility,p.proparallel::text AS parallel,p.prosecdef AS definer,p.prosrc AS source,p.probin AS binary,l.lanname::text AS language,pg_get_userbyid(p.proowner)=current_user AS owner FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_language l ON l.oid=p.prolang WHERE n.nspname='bc_part' AND p.proname IN ('pg_buffercache_evict','pg_buffercache_evict_relation','pg_buffercache_evict_all') ORDER BY p.proname,p.proargtypes::text"
			bcBoth(t, p, b, c, fixture.Select(q,
				[]string{"name", "nargs", "args", "result", "allargs", "modes", "names", "sets", "strict", "volatility", "parallel", "definer", "source", "binary", "language", "owner"},
				[]string{"TEXT", "INT4", "TEXT", "INT8", "TEXT", "TEXT", "TEXT", "BOOL", "BOOL", "TEXT", "TEXT", "BOOL", "TEXT", "TEXT", "TEXT", "BOOL"},
				[][]any{
					{"pg_buffercache_evict", int64(1), "23", int64(2249), "{23,16,16}", "{i,o,o}", "{\"\",buffer_evicted,buffer_flushed}", false, true, "v", "s", false, "pg_buffercache_evict", "$libdir/pg_buffercache", "c", true},
					{"pg_buffercache_evict_all", int64(0), "", int64(2249), "{23,23,23}", "{o,o,o}", "{buffers_evicted,buffers_flushed,buffers_skipped}", false, false, "v", "s", false, "pg_buffercache_evict_all", "$libdir/pg_buffercache", "c", true},
					{"pg_buffercache_evict_relation", int64(1), "2205", int64(2249), "{2205,23,23,23}", "{i,o,o,o}", "{\"\",buffers_evicted,buffers_flushed,buffers_skipped}", false, true, "v", "s", false, "pg_buffercache_evict_relation", "$libdir/pg_buffercache", "c", true},
				}))
			for _, signature := range []string{"pg_buffercache_evict(integer)", "pg_buffercache_evict_relation(regclass)", "pg_buffercache_evict_all()"} {
				q := "SELECT count(*) AS n FROM pg_depend WHERE classid=1255 AND objid=$1::regprocedure AND objsubid=0 AND refclassid=3079 AND refobjid=(SELECT oid FROM pg_extension WHERE extname='pg_buffercache') AND refobjsubid=0 AND deptype='e'"
				bcBoth(t, p, b, c, fixture.Select(q, []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}, "bc_part."+signature))
			}
		})
		for _, spec := range []struct {
			name, formal string
			single       bool
		}{
			{"pg_buffercache_evict", "integer", true},
			{"pg_buffercache_evict_relation", "regclass", false},
		} {
			t.Run(spec.name+"_strict_null", func(t *testing.T) {
				for _, arg := range []string{"NULL", "NULL::" + spec.formal, "$1::" + spec.formal} {
					var args []any
					if arg[0] == '$' {
						args = []any{nil}
					}
					q := "SELECT * FROM bc_part." + spec.name + "(" + arg + ")"
					s := bceNullStep(q, spec.single, args...)
					s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { bcMetadata(t, rows, b.PG, false, s.Columns, s.Types) }
					bcBoth(t, p, b, c, s)
					bcRun(t, p, b, bceHeld(t, p, c, q), s)
					s.Query += " WHERE false"
					s.Rows = nil
					bcBoth(t, p, b, c, s)
					q = "SELECT bc_part." + spec.name + "(" + arg + ") AS result"
					bcBoth(t, p, b, c, bceScalarMetadata(fixture.Select(q, []string{"result"}, []string{"RECORD"}, [][]any{{nil}}, args...), b.PG))
				}
				q := "SELECT e.* FROM (VALUES (NULL::" + spec.formal + ")) AS v(x),bc_part." + spec.name + "(v.x) AS e"
				bcBoth(t, p, b, c, bceNullStep(q, spec.single))
				q = "SELECT bc_part." + spec.name + "(v.x) AS result FROM (VALUES (NULL::" + spec.formal + ")) AS v(x)"
				bcBoth(t, p, b, c, bceScalarMetadata(fixture.Select(q, []string{"result"}, []string{"RECORD"}, [][]any{{nil}}), b.PG))
			})
		}
		t.Run("scalar_and_aliases", func(t *testing.T) {
			bcDDL(t, p, b, c, "CREATE VIEW bc_part.empty_relation AS SELECT 1 AS k")
			bcBoth(t, p, b, c, bceScalarMetadata(fixture.Select("SELECT bc_part.pg_buffercache_evict_relation('bc_part.empty_relation') AS result", []string{"result"}, []string{"RECORD"}, [][]any{{"(0,0,0)"}}), b.PG))
			bcBoth(t, p, b, c, fixture.Select("SELECT e.a,e.b,e.c FROM bc_part.pg_buffercache_evict_relation('bc_part.empty_relation') AS e(a,b,c)", []string{"a", "b", "c"}, bceCountTypes, [][]any{{int64(0), int64(0), int64(0)}}))
			bcBoth(t, p, b, c, fixture.Select("SELECT (e.result).buffers_evicted AS n FROM (SELECT bc_part.pg_buffercache_evict_relation('bc_part.empty_relation') AS result) AS e", []string{"n"}, []string{"INT4"}, [][]any{{int64(0)}}))
			// An unused slot is stable in the clean native model. PG's empty
			// candidate is selected immediately from the real shared pool.
			unused := bcQuery(t, p, b, c, "SELECT * FROM bc_part.pg_buffercache WHERE relfilenode IS NULL LIMIT 1", true)
			if len(unused) != 1 {
				t.Fatal("required unused slot", unused)
			}
			q := "SELECT bc_part.pg_buffercache_evict($1::integer) AS result"
			bcBoth(t, p, b, c, bceScalarMetadata(fixture.Select(q, []string{"result"}, []string{"RECORD"}, [][]any{{"(f,f)"}}, unused[0][0]), b.PG))
			all := bceCall(t, p, b, c, false, false, "SELECT * FROM bc_part.pg_buffercache_evict_all()")
			bceCounts(t, b, all, bcCapacity(t, p, c), 0, 0)
		})
		t.Run("prepare_describe_does_not_mutate", func(t *testing.T) {
			bceHeap(t, p, b, c, "shape_heap")
			bceWarm(t, p, b, c, "shape_heap")
			before := bcTarget(t, p, b, c, "bc_part.shape_heap")
			q := "SELECT * FROM bc_part.pg_buffercache_evict_relation($1::regclass)"
			h := bceHeld(t, p, c, q)
			if after := bcTarget(t, p, b, c, "bc_part.shape_heap"); len(after) != len(before) {
				t.Fatal("Prepare evicted actual storage", before, after)
			}
			for range 2 {
				bceWarm(t, p, b, c, "shape_heap")
				rows := bceCall(t, p, b, h, false, false, q, "bc_part.shape_heap")
				bceCounts(t, b, rows, bcCapacity(t, p, c), 1, 0)
				bceCold(t, p, b, c, "shape_heap")
			}
		})
		t.Run("scalar_direct_fresh_and_held", func(t *testing.T) {
			bceHeap(t, p, b, c, "scalar_heap")
			for _, spec := range []struct {
				name, formal string
				single       bool
			}{
				{"pg_buffercache_evict", "integer", true},
				{"pg_buffercache_evict_relation", "regclass", false},
				{"pg_buffercache_evict_all", "", false},
			} {
				t.Run(spec.name, func(t *testing.T) {
					q := "SELECT bc_part." + spec.name + "() AS result"
					if spec.formal != "" {
						q = "SELECT bc_part." + spec.name + "($1::" + spec.formal + ") AS result"
					}
					held := bceHeld(t, p, c, q)
					for _, route := range []string{"direct", "fresh", "held", "held_again"} {
						t.Run(route, func(t *testing.T) {
							warm := bceWarm(t, p, b, c, "scalar_heap")
							bceClean(t, p, b, c)
							var args []any
							if spec.single {
								args = []any{bceMainSlot(t, warm)}
							} else if spec.formal != "" {
								args = []any{"bc_part.scalar_heap"}
							}
							var source fixture.Executor = c
							if route == "fresh" {
								st, err := c.PrepareContext(p.Ctx, q)
								fixture.State(t, err, "")
								defer st.Close()
								source = fixture.HeldStatement{Executor: c, Statement: st, SQL: q}
							} else if route != "direct" {
								source = held
							}
							n := int64(1)
							if spec.formal == "" && !b.PG {
								n = bceOccupied(t, p, b, c)
							}
							got := bceScalarCall(t, p, b, source, spec.single, q, args...)
							if spec.single {
								bceExact(t, got, true, false)
							} else {
								bceCounts(t, b, got, bcCapacity(t, p, c), n, 0)
							}
							bceCold(t, p, b, c, "scalar_heap")
							bcReadHeap(t, p, b, c, "SELECT k FROM bc_part.scalar_heap")
							bcUsage(t, p, b, c, "bc_part.scalar_heap", 1)
						})
					}
				})
			}
		})
		t.Run("once_per_operand_and_occurrence", func(t *testing.T) {
			bcDDL(t, p, b, c, "CREATE SEQUENCE bc_part.evict_argument")
			for _, prepared := range []bool{false, true} {
				bcDDL(t, p, b, c, "ALTER SEQUENCE bc_part.evict_argument RESTART WITH 1")
				bceCall(t, p, b, c, prepared, true, "SELECT * FROM bc_part.pg_buffercache_evict(nextval('bc_part.evict_argument')::integer)")
				bcBoth(t, p, b, c, fixture.Select("SELECT last_value FROM bc_part.evict_argument", []string{"last_value"}, []string{"INT8"}, [][]any{{int64(1)}}))
			}
		})
	})
}
