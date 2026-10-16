package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// These are execution witnesses, not catalog mapping assertions. The released
// declarations distinguish ANY from ANYELEMENT, compatible arrays from ANYARRAY,
// defaults from written arity, and lowered syntax from similarly named routines:
// https://github.com/postgres/postgres/blob/REL_18_0/src/include/catalog/pg_proc.dat
// https://github.com/postgres/postgres/blob/REL_18_0/src/backend/catalog/system_functions.sql
// https://github.com/postgres/postgres/blob/REL_18_0/src/backend/parser/gram.y
func TestReferenceObjectGrantRoutineFormalSelection(t *testing.T) {
	objectGrantScenario(t, []string{"og_formal_ext"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		run(fixture.Exec("SET TIME ZONE 'UTC'", 0))
		run(fixture.Exec("GRANT USAGE ON SCHEMA public TO og_object_reader", 0))
		run(fixture.Exec("CREATE TABLE og_formal_source(id INT,x INT,s TEXT,a INT[],ts TIMESTAMP,b BYTEA)", 0))
		run(fixture.Exec(`INSERT INTO og_formal_source VALUES(1,10,'abc',ARRAY[10,20,10],TIMESTAMP '2000-01-02 03:04:05','\x616263'),(2,NULL,'def',ARRAY[20],TIMESTAMP '2000-01-02 03:04:05','\x646566'),(3,30,'ghi',ARRAY[30],TIMESTAMP '2000-01-02 03:04:05','\x676869')`, 3))
		run(fixture.Exec("GRANT SELECT ON og_formal_source TO og_object_reader", 0))
		run(fixture.Exec("CREATE SCHEMA og_formal_ext", 0))
		run(fixture.Exec("CREATE EXTENSION pgcrypto SCHEMA og_formal_ext", 0))
		run(fixture.Exec("GRANT USAGE ON SCHEMA og_formal_ext TO og_object_reader", 0))

		one := func(q, typ string, value any) fixture.Step {
			return fixture.Select(q, []string{"v"}, []string{typ}, [][]any{{value}})
		}
		const source = " FROM og_formal_source WHERE id=1"
		const whole = " OVER(ORDER BY id ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING)"
		const windowSource = " AS v FROM og_formal_source ORDER BY id"
		window := func(expr string, rows [][]any) fixture.Step {
			return fixture.Select("SELECT "+expr+whole+windowSource, []string{"v"}, []string{"INT4"}, rows)
		}
		// Alternatives isolate selected declarations, except quote_literal's
		// source-declared SQL wrapper dependency: ANYELEMENT invokes TEXT
		// (REL_18_0 pg_proc.dat, OIDs1285/1283). That dependency is asymmetric.
		// Mirrored pairs also distinguish empty explicit VARIADIC arrays.
		cases := []struct {
			label, target, kind, name string
			want, alternative         fixture.Step
			admission                 string
		}{
			{
				"expression-count-any", "pg_catalog.count(\"any\")", "aggregate", "count",
				one("SELECT count(x) AS v FROM og_formal_source", "INT8", int64(2)),
				one("SELECT count(*) AS v FROM og_formal_source", "INT8", int64(3)),
				"SELECT count(x) AS v FROM og_formal_source WHERE FALSE",
			},
			{
				"array-compatible-two", "pg_catalog.array_position(anycompatiblearray,anycompatible)", "function", "array_position",
				one("SELECT array_position(a,x) AS v"+source, "INT4", int64(1)),
				one("SELECT array_position(a,x,2) AS v"+source, "INT4", int64(3)),
				"SELECT array_position(a,x) AS v FROM og_formal_source WHERE FALSE",
			},
			{
				"array-compatible-three", "pg_catalog.array_position(anycompatiblearray,anycompatible,integer)", "function", "array_position",
				one("SELECT array_position(a,x,2) AS v"+source, "INT4", int64(3)),
				one("SELECT array_position(a,x) AS v"+source, "INT4", int64(1)),
				"SELECT array_position(a,x,2) AS v FROM og_formal_source WHERE FALSE",
			},
			{
				"quote-text", "pg_catalog.quote_literal(text)", "function", "quote_literal",
				one("SELECT quote_literal(s) AS v"+source, "TEXT", "'abc'"),
				one("SELECT quote_literal(x) AS v"+source, "TEXT", "'10'"),
				"SELECT quote_literal(s) AS v FROM og_formal_source WHERE FALSE",
			},
			{
				"quote-anyelement", "pg_catalog.quote_literal(anyelement)", "function", "quote_literal",
				one("SELECT quote_literal(x) AS v"+source, "TEXT", "'10'"),
				one("SELECT quote_literal(s) AS v"+source, "TEXT", "'abc'"),
				"SELECT quote_literal(x) AS v FROM og_formal_source WHERE FALSE",
			},
			{
				"format-single", "pg_catalog.format(text)", "function", "format",
				one("SELECT format(s) AS v"+source, "TEXT", "abc"),
				one("SELECT format(s,VARIADIC ARRAY[]::text[]) AS v"+source, "TEXT", "abc"),
				"SELECT format(s) AS v FROM og_formal_source WHERE FALSE",
			},
			{
				"format-variadic-empty", "pg_catalog.format(text,\"any\")", "function", "format",
				one("SELECT format(s,VARIADIC ARRAY[]::text[]) AS v"+source, "TEXT", "abc"),
				one("SELECT format(s) AS v"+source, "TEXT", "abc"),
				"SELECT format(s,VARIADIC ARRAY[]::text[]) AS v FROM og_formal_source WHERE FALSE",
			},
			{
				"json-empty", "pg_catalog.jsonb_build_array()", "function", "jsonb_build_array",
				one("SELECT jsonb_build_array() AS v", "JSONB", "[]"),
				one("SELECT jsonb_build_array(VARIADIC ARRAY[]::integer[]) AS v", "JSONB", "[]"),
				"SELECT jsonb_build_array() AS v FROM og_formal_source WHERE FALSE",
			},
			{
				"json-variadic-empty", "pg_catalog.jsonb_build_array(\"any\")", "function", "jsonb_build_array",
				one("SELECT jsonb_build_array(VARIADIC ARRAY[]::integer[]) AS v", "JSONB", "[]"),
				one("SELECT jsonb_build_array() AS v", "JSONB", "[]"),
				"SELECT jsonb_build_array(VARIADIC ARRAY[]::integer[]) AS v FROM og_formal_source WHERE FALSE",
			},
			{
				"normalize-written-one-formal-two", "pg_catalog.normalize(text,text)", "function", "normalize",
				one("SELECT normalize(s) AS v"+source, "TEXT", "abc"),
				one("SELECT is_normalized(s) AS v"+source, "BOOL", true),
				"SELECT normalize(s) AS v FROM og_formal_source WHERE FALSE",
			},
			{
				"make-interval-written-zero-formal-seven", "pg_catalog.make_interval(integer,integer,integer,integer,integer,integer,double precision)", "function", "make_interval",
				one("SELECT make_interval()::text AS v", "TEXT", "00:00:00"),
				one("SELECT make_time(1,2,3)::text AS v", "TEXT", "01:02:03"),
				"",
			},
			{
				"position-lowering-not-strpos", "pg_catalog.position(text,text)", "function", "position",
				one("SELECT POSITION('b' IN s) AS v"+source, "INT4", int64(2)),
				one("SELECT strpos(s,'b') AS v"+source, "INT4", int64(2)),
				"SELECT POSITION('b' IN s) AS v FROM og_formal_source WHERE FALSE",
			},
			{
				"at-time-zone-text-formal", "pg_catalog.timezone(text,timestamp without time zone)", "function", "timezone",
				one("SELECT (ts AT TIME ZONE 'UTC')::text AS v"+source, "TEXT", "2000-01-02 03:04:05+00"),
				one("SELECT (ts AT TIME ZONE INTERVAL '0')::text AS v"+source, "TEXT", "2000-01-02 03:04:05+00"),
				"SELECT ts AT TIME ZONE 'UTC' AS v FROM og_formal_source WHERE FALSE",
			},
			{
				"first-value-anyelement", "pg_catalog.first_value(anyelement)", "function", "first_value",
				window("first_value(x)", [][]any{{int64(10)}, {int64(10)}, {int64(10)}}),
				window("last_value(x)", [][]any{{int64(30)}, {int64(30)}, {int64(30)}}),
				"SELECT first_value(x) OVER() AS v FROM og_formal_source WHERE FALSE",
			},
			{
				"last-value-anyelement", "pg_catalog.last_value(anyelement)", "function", "last_value",
				window("last_value(x)", [][]any{{int64(30)}, {int64(30)}, {int64(30)}}),
				window("first_value(x)", [][]any{{int64(10)}, {int64(10)}, {int64(10)}}),
				"SELECT last_value(x) OVER() AS v FROM og_formal_source WHERE FALSE",
			},
			{
				"nth-value-anyelement-int4", "pg_catalog.nth_value(anyelement,integer)", "function", "nth_value",
				window("nth_value(x,2)", [][]any{{nil}, {nil}, {nil}}),
				window("first_value(x)", [][]any{{int64(10)}, {int64(10)}, {int64(10)}}),
				"SELECT nth_value(x,0) OVER() AS v FROM og_formal_source WHERE FALSE",
			},
			{
				"pgcrypto-text-not-bytea", "og_formal_ext.digest(text,text)", "function", "digest",
				one("SELECT encode(og_formal_ext.digest(s,'sha256'),'hex') AS v"+source, "TEXT", "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"),
				one("SELECT encode(og_formal_ext.digest(b,'sha256'),'hex') AS v"+source, "TEXT", "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"),
				"SELECT og_formal_ext.digest(s,'sha256') AS v FROM og_formal_source WHERE FALSE",
			},
		}
		for _, tc := range cases {
			if !t.Run(tc.label, func(t *testing.T) {
				run := func(s fixture.Step) {
					t.Helper()
					s.Prepared = prepared
					if !t.Run(s.Query, func(t *testing.T) {
						fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					}) {
						t.FailNow()
					}
				}
				// Cleanup runs even after a fatal expectation failure and before the
				// next case/backend. public-schema resets cannot restore pg_catalog ACLs.
				defer objectGrantRoutineCleanup(t, p, c, []string{tc.target})
				query := tc.want.Query
				stmt, err := c.PrepareContext(p.Ctx, query)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
				runHeld := func(s fixture.Step) {
					t.Helper()
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				}
				fixture.RoleGrantActor(run, "og_object_reader", func() {
					run(tc.want)
					run(tc.alternative)
					runHeld(tc.want)
				})
				run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+tc.target+" FROM PUBLIC", 0))
				fixture.RoleGrantActor(run, "og_object_reader", func() {
					denied := fixture.ImplicitError(query, fixture.Diagnostic{Code: "42501", Message: "permission denied for " + tc.kind + " " + tc.name})
					denied.SelectRows, denied.ErrorStage = true, "execute"
					run(denied)
					if b.PG && tc.label == "quote-anyelement" {
						// PG18 can retain the builtin SQL wrapper's inlined
						// TEXT call in this already-executed held plan. Its
						// clauses.c inline_function checks X only before
						// inlining; setrefs.c record_plan_function_dependency
						// omits pinned builtin OIDs (<12000), including1285.
						// Native intentionally rechecks the captured wrapper
						// identity's fresh ACL: it has no executable plan
						// cache and must not reproduce this PG cache boundary.
						runHeld(tc.want)
					} else if b.PG && tc.label == "make-interval-written-zero-formal-seven" {
						// Unlike SQL-wrapper inlining above, this immutable
						// builtin call has seven constant default arguments
						// and PG18 evaluate_function folds it to zero INTERVAL.
						// The old held plan retains that constant after revoke;
						// builtin3464 likewise has no invalidation dependency.
						// Native retains fresh captured-identity ACL admission,
						// not an executable plan cache or this folding exception.
						runHeld(tc.want)
					} else {
						runHeld(denied)
					}
					// Parse/Describe is effect-free even with the exact X denied;
					// the new prepared consumer fails only when it is executed.
					fresh, err := c.PrepareContext(p.Ctx, query)
					if err != nil {
						t.Fatal("Prepare while EXECUTE denied", err)
					}
					defer fresh.Close()
					freshHeld := fixture.HeldStatement{Executor: c, Statement: fresh, SQL: query}
					fixture.RunStep(t, p.Ctx, freshHeld, b.Recorder, denied)
					if err := fresh.Close(); err != nil {
						t.Fatal(err)
					}
					if tc.admission != "" {
						empty := fixture.ImplicitError(tc.admission, fixture.Diagnostic{Code: "42501", Message: "permission denied for " + tc.kind + " " + tc.name})
						empty.SelectRows, empty.ErrorStage = true, "execute"
						run(empty)
					}
					if tc.label == "quote-text" {
						// The released ANYELEMENT SQL body calls the denied
						// TEXT overload as invoker; it is not an independent
						// unaffected execution path (root oracle artifact206).
						dependent := fixture.ImplicitError(tc.alternative.Query, fixture.Diagnostic{Code: "42501", Message: "permission denied for function quote_literal"})
						dependent.SelectRows, dependent.ErrorStage = true, "execute"
						run(dependent)
					} else {
						run(tc.alternative)
					}
				})
				run(fixture.Exec("GRANT EXECUTE ON ROUTINE "+tc.target+" TO og_object_reader", 0))
				fixture.RoleGrantActor(run, "og_object_reader", func() {
					run(tc.want)
					run(tc.alternative)
					runHeld(tc.want)
				})
			}) {
				t.FailNow()
			}
		}
	})
}
