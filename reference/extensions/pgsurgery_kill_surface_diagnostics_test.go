package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgsurgeryKillDiagnostics(t *testing.T) {
	t.Run("relation_kinds", func(t *testing.T) {
		cleanAM := fixture.AccessMethodReferenceCleanup(t, []string{"ks_nonheap"})
		ksCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			if b.PG {
				cleanAM()
			}
			defer c.ExecContext(p.Ctx, "DROP ACCESS METHOD IF EXISTS ks_nonheap CASCADE")
			defer c.ExecContext(p.Ctx, "DROP FOREIGN DATA WRAPPER IF EXISTS ks_fdw CASCADE")
			for _, q := range []string{
				"CREATE VIEW ks_part.v AS SELECT k FROM ks_part.heap", "CREATE SEQUENCE ks_part.seq",
				"CREATE INDEX ks_index ON ks_part.heap(k)", "CREATE TYPE ks_part.composite AS(k integer)",
				"CREATE TABLE ks_part.parent(k integer) PARTITION BY RANGE(k)",
				"CREATE UNIQUE INDEX ks_parent_index ON ks_part.parent(k)",
				"CREATE FOREIGN DATA WRAPPER ks_fdw", "CREATE SERVER ks_server FOREIGN DATA WRAPPER ks_fdw",
				"CREATE FOREIGN TABLE ks_part.ft(k integer) SERVER ks_server",
				"CREATE MATERIALIZED VIEW ks_part.mv AS SELECT k FROM ks_part.heap",
				"CREATE UNLOGGED TABLE ks_part.unlogged(k integer)", "CREATE TEMP TABLE ks_temp(k integer)",
				"CREATE ACCESS METHOD ks_nonheap TYPE TABLE HANDLER heap_tableam_handler",
				"CREATE TABLE ks_part.nonheap(k integer)",
				"ALTER TABLE ks_part.nonheap SET ACCESS METHOD ks_nonheap",
			} {
				freezeRun(t, p, b, c, fixture.Exec(q, 0))
			}
			a := freezeOne(t, p, c, "INSERT INTO ks_part.heap VALUES(1,'unchanged') RETURNING k,v,ctid,xmin,xmax")
			for _, tc := range []struct{ name, kind string }{{"v", "views"}, {"seq", "sequences"}, {"ks_index", "indexes"}, {"composite", "composite types"}, {"parent", "partitioned tables"}, {"ks_parent_index", "partitioned indexes"}, {"ft", "foreign tables"}} {
				t.Run(tc.name, func(t *testing.T) {
					s := ksError(killSQL, "42809", "cannot operate on relation \""+tc.name+"\"", "ks_part."+tc.name, "{}")
					s.Diagnostic.Detail = "This operation is not supported for " + tc.kind + "."
					s.ErrorStage = "execute"
					freezeBoth(t, p, b, c, s)
					ksRows(t, p, b, c, a)
				})
			}
			freezeBoth(t, p, b, c, ksError(killSQL, "0A000", "only heap AM is supported", "ks_part.nonheap", "{}"))
			for _, target := range []string{"ks_part.heap", "ks_part.mv", "ks_part.unlogged", "ks_temp"} {
				freezeBoth(t, p, b, c, ksVoid(killSQL, target, "{}"))
			}
			ksRows(t, p, b, c, a)
		})
	})
	t.Run("coercion_and_tid", func(t *testing.T) {
		ksCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			a := freezeOne(t, p, c, "INSERT INTO ks_part.heap VALUES(1,'unchanged') RETURNING k,v,ctid,xmin,xmax")
			freezeBoth(t, p, b, c, ksError(killSQL, "XX000", "could not open relation with OID 0", int64(0), "{}"))
			for _, array := range []any{"{}", nil} {
				q := "SELECT ks_part.heap_force_kill($1::text::regclass,$2::tid[]) AS killed"
				if array == nil {
					freezeBoth(t, p, b, c, fixture.Select(q, []string{"killed"}, []string{"VOID"}, [][]any{{nil}}, "ks_part.absent", array))
				} else {
					freezeBoth(t, p, b, c, ksError(q, "42P01", "relation \"ks_part.absent\" does not exist", "ks_part.absent", array))
				}
			}
			for _, bound := range []struct {
				target, tids, code, message, context string
			}{
				{"ks_part.absent", "{}", "42P01", "relation \"ks_part.absent\" does not exist", "unnamed portal parameter $1 = '...'"},
				{"ks_part.heap", "{bad}", "22P02", "invalid input syntax for type tid: \"bad\"", "unnamed portal parameter $2 = '...'"},
			} {
				s := ksError(killSQL, bound.code, bound.message, bound.target, bound.tids)
				s.Diagnostic.Context, s.ErrorStage = bound.context, "execute"
				freezeBoth(t, p, b, c, s)
				ksRows(t, p, b, c, a)
			}
			literal := "SELECT ks_part.heap_force_kill('ks_part.absent'::regclass,NULL::tid[]) AS killed"
			missing := ksError(literal, "42P01", "relation \"ks_part.absent\" does not exist")
			missing.Diagnostic.Position = fixture.Position(strings.Index(literal, "'ks_part.absent'") + 1)
			ksAnalysisError(t, p, b, c, missing)
			for _, input := range []string{"", "bad", "(1)", "(1,2", "(1,2,3)", "(4294967296,1)", "(-2147483649,1)", "(1,65536)", "(1,-1)", "(18446744073709551616,1)", "(1 ,2)", "(1,2 )"} {
				t.Run(input, func(t *testing.T) {
					q := "SELECT ks_part.heap_force_kill('ks_part.heap'::regclass,ARRAY[$1::text::tid]) AS killed"
					s := ksError(q, "22P02", "invalid input syntax for type tid: \""+input+"\"", input)
					s.ErrorStage = "execute"
					freezeBoth(t, p, b, c, s)
					q = "SELECT ks_part.heap_force_kill('ks_part.heap'::regclass,ARRAY[$1::tid]) AS killed"
					s.Query = q
					s.Diagnostic.Context = "unnamed portal parameter $1 = '...'"
					if input == "" {
						s.Diagnostic.Context = "unnamed portal parameter $1 = ''"
					}
					freezeBoth(t, p, b, c, s)
					ksRows(t, p, b, c, a)
				})
			}
			q := "SELECT ks_part.heap_force_kill('ks_part.heap'::regclass,ARRAY['bad'::tid]) AS killed"
			s := ksError(q, "22P02", "invalid input syntax for type tid: \"bad\"")
			s.Diagnostic.Position = fixture.Position(strings.Index(q, "'bad'") + 1)
			ksAnalysisError(t, p, b, c, s)
			ksRows(t, p, b, c, a)
		})
	})
	t.Run("binder_decorations", func(t *testing.T) {
		ksCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			for _, tc := range []struct{ call, signature string }{
				{"()", ""}, {"(*)", ""}, {"('ks_part.heap'::regclass)", "regclass"},
				{"('ks_part.heap'::regclass,1)", "regclass, integer"},
				{"('ks_part.heap'::regclass,'{}'::tid[],1)", "regclass, tid[], integer"},
				{"(VARIADIC ARRAY[1])", "integer[]"},
				{"(wrong=>'ks_part.heap'::regclass,tids=>'{}'::tid[])", "wrong => regclass, tids => tid[]"},
			} {
				q := "SELECT ks_part.heap_force_kill" + tc.call
				s := ksError(q, "42883", "function ks_part.heap_force_kill("+tc.signature+") does not exist")
				s.Diagnostic.Position = "8"
				s.Diagnostic.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
				ksAnalysisError(t, p, b, c, s)
			}
			q := "SELECT ks_part.heap_force_kill(reloid=>'ks_part.heap'::regclass,tids=>'{}'::tid[],tids=>'{}'::tid[])"
			s := ksError(q, "42601", "argument name \"tids\" used more than once")
			s.Diagnostic.Position = fixture.Position(strings.LastIndex(q, "tids=>") + 1)
			ksAnalysisError(t, p, b, c, s)
			call := "ks_part.heap_force_kill('ks_part.heap'::regclass,'{}'::tid[])"
			for _, tc := range []struct{ q, message string }{
				{"SELECT ks_part.heap_force_kill(DISTINCT 'ks_part.heap'::regclass,'{}'::tid[])", "DISTINCT specified, but ks_part.heap_force_kill is not an aggregate function"},
				{"SELECT ks_part.heap_force_kill('ks_part.heap'::regclass,'{}'::tid[] ORDER BY 1)", "ORDER BY specified, but ks_part.heap_force_kill is not an aggregate function"},
				{"SELECT " + call + " FILTER (WHERE true)", "FILTER specified, but ks_part.heap_force_kill is not an aggregate function"},
				{"SELECT " + call + " OVER ()", "OVER specified, but ks_part.heap_force_kill is not a window function nor an aggregate function"},
			} {
				s := ksError(tc.q, "42809", tc.message)
				s.Diagnostic.Position = "8"
				ksAnalysisError(t, p, b, c, s)
			}
		})
	})
}

func ksAnalysisError(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, s fixture.Step) {
	t.Helper()
	s.ErrorStage = "execute"
	freezeRun(t, p, b, c, s)
	s.Prepared, s.ErrorStage = true, "prepare"
	freezeRun(t, p, b, c, s)
}
