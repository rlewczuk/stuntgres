package reference_test

import (
	"bytes"
	"strconv"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// Candidates follow REL_18_6 parse_coerce.c:coerce_to_boolean (Assignment).
// Every diagnostic field, including the expression position, is asserted; the
// first pinned oracle run must resolve candidates rather than learn from native.
func TestReferenceCastDefinitionBooleanContexts(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			conn, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			run := func(s fixture.Step) {
				t.Helper()
				fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s)
			}
			for _, q := range []string{
				castDefinitionSource,
				`CREATE FUNCTION cast_truth(cast_src) RETURNS boolean LANGUAGE SQL AS $$ SELECT CASE WHEN $1 IS NULL THEN true WHEN $1::text='red' THEN true ELSE false END $$`,
			} {
				run(fixture.Exec(q, 0))
			}
			cases := []struct {
				name, query, construct string
				red, blue, null        [][]any
				typ                    string
			}{
				{"where", "SELECT 1 AS v WHERE $1::cast_src", "WHERE", [][]any{{int64(1)}}, [][]any{}, [][]any{{int64(1)}}, "INT4"},
				{"and", "SELECT ($1::cast_src AND true) AS v", "AND", [][]any{{true}}, [][]any{{false}}, [][]any{{true}}, "BOOL"},
				{"or", "SELECT ($1::cast_src OR false) AS v", "OR", [][]any{{true}}, [][]any{{false}}, [][]any{{true}}, "BOOL"},
				{"not", "SELECT (NOT $1::cast_src) AS v", "NOT", [][]any{{false}}, [][]any{{true}}, [][]any{{false}}, "BOOL"},
				{"case", "SELECT CASE WHEN $1::cast_src THEN 1 ELSE 0 END AS v", "CASE/WHEN", [][]any{{int64(1)}}, [][]any{{int64(0)}}, [][]any{{int64(1)}}, "INT4"},
				{"is_true", "SELECT ($1::cast_src IS TRUE) AS v", "IS TRUE", [][]any{{true}}, [][]any{{false}}, [][]any{{true}}, "BOOL"},
				{"is_not_true", "SELECT ($1::cast_src IS NOT TRUE) AS v", "IS NOT TRUE", [][]any{{false}}, [][]any{{true}}, [][]any{{false}}, "BOOL"},
				{"is_false", "SELECT ($1::cast_src IS FALSE) AS v", "IS FALSE", [][]any{{false}}, [][]any{{true}}, [][]any{{false}}, "BOOL"},
				{"is_not_false", "SELECT ($1::cast_src IS NOT FALSE) AS v", "IS NOT FALSE", [][]any{{true}}, [][]any{{false}}, [][]any{{true}}, "BOOL"},
				{"is_unknown", "SELECT ($1::cast_src IS UNKNOWN) AS v", "IS UNKNOWN", [][]any{{false}}, [][]any{{false}}, [][]any{{false}}, "BOOL"},
				{"is_not_unknown", "SELECT ($1::cast_src IS NOT UNKNOWN) AS v", "IS NOT UNKNOWN", [][]any{{true}}, [][]any{{true}}, [][]any{{true}}, "BOOL"},
				{"join", "SELECT a.v AS v FROM (VALUES (1)) a(v) JOIN (VALUES (2)) b(v) ON $1::cast_src", "JOIN/ON", [][]any{{int64(1)}}, [][]any{}, [][]any{{int64(1)}}, "INT4"},
				{"having", "SELECT count(*) AS v FROM (VALUES (1)) a(v) HAVING $1::cast_src", "HAVING", [][]any{{int64(1)}}, [][]any{}, [][]any{{int64(1)}}, "INT8"},
				{"filter", "SELECT count(*) FILTER (WHERE $1::cast_src) AS v FROM (VALUES (1)) a(v)", "FILTER", [][]any{{int64(1)}}, [][]any{{int64(0)}}, [][]any{{int64(1)}}, "INT8"},
			}
			for _, mode := range []struct{ name, suffix, context string }{
				{"explicit", "", "e"}, {"assignment", " AS ASSIGNMENT", "a"}, {"implicit", " AS IMPLICIT", "i"},
			} {
				run(fixture.Exec("CREATE CAST (cast_src AS boolean) WITH FUNCTION cast_truth(cast_src)"+mode.suffix, 0))
				for _, tc := range cases {
					t.Run(mode.name+"/"+tc.name, func(t *testing.T) {
						if mode.context == "e" {
							for _, prepared := range []bool{false, true} {
								t.Run(strconv.FormatBool(prepared), func(t *testing.T) {
									d := fixture.ControlsReferenceDiagnostic("42804", "argument of "+tc.construct+" must be type boolean, not type cast_src")
									d.Position = strconv.Itoa(strings.Index(tc.query, "$1") + 1)
									s := fixture.ErrorDiagnostic(tc.query, d, "red")
									s.Prepared, s.ErrorStage = prepared, "execute"
									if prepared {
										s.ErrorStage = "prepare"
									}
									fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s)
								})
							}
							return
						}
						stmt, err := conn.PrepareContext(p.Ctx, tc.query)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
						held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: tc.query}
						for _, input := range []struct {
							value any
							rows  [][]any
						}{{"red", tc.red}, {"blue", tc.blue}, {nil, tc.null}} {
							s := fixture.Select(tc.query, []string{"v"}, []string{tc.typ}, input.rows, input.value)
							fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s)
							fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Prepared(s))
							fixture.RunStep(t, p.Ctx, held, backend.Recorder, s)
						}
					})
				}
				// Explicit syntax remains legal in every context and executes the
				// same nonstrict routine, including the typed NULL witness.
				for _, input := range []struct {
					value any
					want  bool
				}{{"red", true}, {"blue", false}, {nil, true}} {
					run(fixture.Select("SELECT ($1::cast_src)::boolean AS v", []string{"v"}, []string{"BOOL"}, [][]any{{input.want}}, input.value))
				}
				if mode.context != "e" {
					q := "SELECT CASE WHEN $1::cast_src THEN 1 ELSE 0 END AS v,$1::text AS source"
					stmt, err := conn.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}
					fixture.RunStep(t, p.Ctx, held, backend.Recorder, fixture.Select(q, []string{"v", "source"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "red"}}, "red"))
					stmt.Close()
					run(fixture.Exec("CREATE TABLE cast_boolean_check(s cast_src CONSTRAINT cast_boolean_ok CHECK (s))", 0))
					run(fixture.Exec("INSERT INTO cast_boolean_check VALUES ('red'),(NULL)", 2))
					t.Run(mode.name+"/check_atomicity", func(t *testing.T) {
						d := fixture.ControlsReferenceDiagnostic("23514", `new row for relation "cast_boolean_check" violates check constraint "cast_boolean_ok"`)
						d.Schema, d.Table, d.Constraint = "public", "cast_boolean_check", "cast_boolean_ok"
						d.Detail = "Failing row contains (blue)."
						fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.ErrorDiagnostic("INSERT INTO cast_boolean_check VALUES ('red'),('blue')", d))
					})
					run(fixture.Select("SELECT count(*) AS n FROM cast_boolean_check", []string{"n"}, []string{"INT8"}, [][]any{{int64(2)}}))
					run(fixture.Exec("DROP TABLE cast_boolean_check", 0))
					run(fixture.Exec("CREATE TABLE cast_boolean_effect(id integer UNIQUE,v integer)", 0))
					run(fixture.Exec("INSERT INTO cast_boolean_effect VALUES (1,10)", 1))
					run(fixture.Exec("INSERT INTO cast_boolean_effect VALUES (1,20) ON CONFLICT(id) DO UPDATE SET v=excluded.v WHERE 'blue'::cast_src", 0))
					run(fixture.Exec("INSERT INTO cast_boolean_effect VALUES (1,30) ON CONFLICT(id) DO UPDATE SET v=excluded.v WHERE 'red'::cast_src", 1))
					run(fixture.Select("SELECT cast_truth(NULL::cast_src) AS called,(NULL::cast_src)::boolean AS casted,CASE WHEN NULL::cast_src THEN 1 ELSE 0 END AS tested", []string{"called", "casted", "tested"}, []string{"BOOL", "BOOL", "INT4"}, [][]any{{true, true, int64(1)}}))
					run(fixture.Exec("MERGE INTO cast_boolean_effect t USING (VALUES (1)) s(id) ON t.id=s.id WHEN MATCHED AND 'blue'::cast_src THEN UPDATE SET v=40", 0))
					// lib/pq exports MERGE RowsAffected as zero; stored values
					// independently prove the selected nonstrict NULL conversion.
					merge := fixture.Exec("MERGE INTO cast_boolean_effect t USING (VALUES (1)) s(id) ON t.id=s.id WHEN MATCHED AND NULL::cast_src THEN UPDATE SET v=50", 1)
					if backend.PG {
						merge.Affected = 0
					}
					run(merge)
					run(fixture.Select("SELECT v FROM cast_boolean_effect", []string{"v"}, []string{"INT4"}, [][]any{{int64(50)}}))
					merge = fixture.Exec("MERGE INTO cast_boolean_effect t USING (VALUES (1)) s(id) ON t.id=s.id WHEN MATCHED AND 'red'::cast_src THEN UPDATE SET v=60", 1)
					if backend.PG {
						merge.Affected = 0
					}
					run(merge)
					run(fixture.Select("SELECT v FROM cast_boolean_effect", []string{"v"}, []string{"INT4"}, [][]any{{int64(60)}}))
					run(fixture.Exec("DROP TABLE cast_boolean_effect", 0))
				}
				run(fixture.Select("SELECT castcontext::text AS context,castmethod::text AS method,castfunc='cast_truth(cast_src)'::regprocedure::oid AS routine FROM pg_cast WHERE castsource='cast_src'::regtype AND casttarget='boolean'::regtype", []string{"context", "method", "routine"}, []string{"TEXT", "TEXT", "BOOL"}, [][]any{{mode.context, "f", true}}))
				run(fixture.Exec("DROP CAST (cast_src AS boolean)", 0))
				run(fixture.Select("SELECT count(*) AS n FROM pg_cast WHERE castsource='cast_src'::regtype AND casttarget='boolean'::regtype", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
			}
			for _, q := range []string{
				"CREATE CAST (cast_src AS boolean) WITH FUNCTION cast_truth(cast_src) AS ASSIGNMENT",
				"CREATE VIEW cast_boolean_old AS SELECT CASE WHEN 'red'::cast_src THEN 1 ELSE 0 END AS v",
				"DROP CAST (cast_src AS boolean)",
				`CREATE FUNCTION cast_false(cast_src) RETURNS boolean LANGUAGE SQL AS $$ SELECT false $$`,
				"CREATE CAST (cast_src AS boolean) WITH FUNCTION cast_false(cast_src) AS ASSIGNMENT",
			} {
				run(fixture.Exec(q, 0))
			}
			checks := []fixture.Step{
				fixture.Select("SELECT v FROM cast_boolean_old", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}}),
				fixture.Select("SELECT CASE WHEN 'red'::cast_src THEN 1 ELSE 0 END AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(0)}}),
				fixture.Select("SELECT count(*) AS n FROM pg_cast WHERE castsource='cast_src'::regtype AND casttarget='boolean'::regtype AND castfunc='cast_false(cast_src)'::regprocedure::oid AND castcontext::text='a' AND castmethod::text='f'", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}),
			}
			for _, s := range checks {
				run(s)
			}
			if !backend.PG {
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
				for _, s := range checks {
					run(fixture.Prepared(s))
				}
				if p.Postgres != nil {
					if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
						t.Fatal(err)
					}
					if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
						t.Fatalf("actual Boolean dump SQL on PostgreSQL: %v\n%s", err, dump.String())
					}
					for _, s := range checks {
						fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, s)
					}
					if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
						t.Fatal(err)
					}
				}
			}
		})
	}
}
