package reference_test

import (
	"bytes"
	"strconv"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// REL_18_6 functioncmds.c binds DEFAULT through coerce_to_specific_type,
// therefore Assignment context must consult a declared edge before string IO.
func TestReferenceCastDefinitionRoutineDefaults(t *testing.T) {
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
				`CREATE FUNCTION cast_score(cast_src) RETURNS integer LANGUAGE SQL RETURN CASE WHEN $1 IS NULL THEN 99 WHEN $1='red'::cast_src THEN 41 ELSE 42 END`,
				castDefinitionEdge + " AS ASSIGNMENT",
				"CREATE DOMAIN cast_default_int AS integer CHECK (VALUE > 0)",
				`CREATE FUNCTION public.cast_default_receiver(v integer DEFAULT 'red'::cast_src) RETURNS integer LANGUAGE SQL RETURN v`,
				`CREATE FUNCTION public.cast_default_null(v integer DEFAULT NULL::cast_src) RETURNS integer LANGUAGE SQL RETURN v`,
				`CREATE FUNCTION public.cast_default_domain(v cast_default_int DEFAULT 'red'::cast_src) RETURNS integer LANGUAGE SQL RETURN v::integer`,
				"CREATE VIEW cast_default_old_call AS SELECT cast_default_receiver() AS red,cast_default_null() AS null_value",
				`CREATE FUNCTION cast_default_body(cast_src) RETURNS integer LANGUAGE SQL RETURN $1::integer`,
				`CREATE FUNCTION cast_default_text(cast_src) RETURNS text LANGUAGE SQL AS $$ SELECT 'converted'::text $$`,
				"CREATE CAST (cast_src AS text) WITH FUNCTION cast_default_text(cast_src)",
			} {
				run(fixture.Exec(q, 0))
			}
			for _, tc := range []struct {
				query string
				args  []any
				want  int64
			}{
				{"SELECT cast_default_receiver() AS v", nil, 41},
				{"SELECT cast_default_receiver($1) AS v", []any{int64(7)}, 7},
				{"SELECT cast_default_null() AS v", nil, 99},
				{"SELECT cast_default_domain() AS v", nil, 41},
				{"SELECT cast_default_body('red'::cast_src) AS v", nil, 41},
			} {
				stmt, err := conn.PrepareContext(p.Ctx, tc.query)
				if err != nil {
					t.Fatal(err)
				}
				held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: tc.query}
				s := fixture.Select(tc.query, []string{"v"}, []string{"INT4"}, [][]any{{tc.want}}, tc.args...)
				run(s)
				run(fixture.Prepared(s))
				fixture.RunStep(t, p.Ctx, held, backend.Recorder, s)
				stmt.Close()
			}
			// Independently named declarations keep discovery failures from hiding
			// the other route. Empty full fields are candidates, not wildcards.
			for _, prepared := range []bool{false, true} {
				t.Run("explicit_refusal/"+strconv.FormatBool(prepared), func(t *testing.T) {
					name := "cast_default_bad_direct"
					if prepared {
						name = "cast_default_bad_prepared"
					}
					q := "CREATE FUNCTION " + name + "(v text DEFAULT 'red'::cast_src) RETURNS text LANGUAGE SQL RETURN v"
					d := fixture.ControlsReferenceDiagnostic("42804", "argument of DEFAULT must be type text, not type cast_src")
					d.Position = strconv.Itoa(strings.Index(q, "'red'") + 1)
					s := fixture.ErrorDiagnostic(q, d)
					s.Prepared, s.ErrorStage = prepared, "execute"
					fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s)
				})
			}
			run(fixture.Select("SELECT count(*) AS n FROM pg_proc WHERE proname IN ('cast_default_bad_direct','cast_default_bad_prepared')", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
			run(fixture.Exec("CREATE FUNCTION cast_default_explicit(v text DEFAULT ('red'::cast_src)::text) RETURNS text LANGUAGE SQL RETURN v", 0))
			run(fixture.Select("SELECT cast_default_explicit() AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"converted"}}))
			for _, q := range []string{
				"DROP CAST (cast_src AS integer)",
				`CREATE FUNCTION cast_default_new(cast_src) RETURNS integer LANGUAGE SQL AS $$ SELECT CASE WHEN $1 IS NULL THEN 88 ELSE 77 END $$`,
				"CREATE CAST (cast_src AS integer) WITH FUNCTION cast_default_new(cast_src) AS IMPLICIT",
				`CREATE FUNCTION cast_default_fresh(v integer DEFAULT 'red'::cast_src) RETURNS integer LANGUAGE SQL RETURN v`,
				`CREATE FUNCTION cast_default_fresh_null(v integer DEFAULT NULL::cast_src) RETURNS integer LANGUAGE SQL RETURN v`,
			} {
				run(fixture.Exec(q, 0))
			}
			checks := []fixture.Step{
				fixture.Select("SELECT cast_default_receiver() AS red,cast_default_null() AS null_value,cast_default_receiver(7) AS supplied,cast_default_domain() AS domain_value", []string{"red", "null_value", "supplied", "domain_value"}, []string{"INT4", "INT4", "INT4", "INT4"}, [][]any{{int64(41), int64(99), int64(7), int64(41)}}),
				fixture.Select("SELECT red,null_value FROM cast_default_old_call", []string{"red", "null_value"}, []string{"INT4", "INT4"}, [][]any{{int64(41), int64(99)}}),
				fixture.Select("SELECT cast_default_fresh() AS red,cast_default_fresh_null() AS null_value,cast_default_body('red'::cast_src) AS body", []string{"red", "null_value", "body"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(77), int64(88), int64(41)}}),
				fixture.Select("SELECT cast_default_explicit() AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"converted"}}),
				fixture.Select("SELECT count(*) AS n FROM pg_cast WHERE castsource='cast_src'::regtype AND casttarget='integer'::regtype AND castfunc='cast_default_new(cast_src)'::regprocedure::oid AND castcontext::text='i' AND castmethod::text='f'", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}),
				fixture.Select("SELECT count(*) AS n FROM pg_cast WHERE castsource='cast_src'::regtype", []string{"n"}, []string{"INT8"}, [][]any{{int64(2)}}),
				fixture.Select("SELECT count(*) AS n FROM pg_proc WHERE pronamespace='public'::regnamespace", []string{"n"}, []string{"INT8"}, [][]any{{int64(10)}}),
				fixture.Select("SELECT count(*) AS n FROM pg_depend WHERE classid='pg_proc'::regclass AND objid='cast_default_receiver(integer)'::regprocedure::oid AND refclassid='pg_proc'::regclass AND refobjid='cast_score(cast_src)'::regprocedure::oid AND deptype='n'", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}),
				fixture.Select("SELECT count(*) AS n FROM pg_depend WHERE classid='pg_proc'::regclass AND objid='cast_default_body(cast_src)'::regprocedure::oid AND refclassid='pg_proc'::regclass AND refobjid='cast_score(cast_src)'::regprocedure::oid AND deptype='n'", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}),
			}
			for _, s := range checks {
				run(s)
			}
			if !backend.PG {
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				// A late invalid declaration cannot replace the installed defaults,
				// body references, domain, or final current edge.
				bad := append(append([]byte(nil), dump.Bytes()...), []byte("\nCREATE CAST (cast_src AS integer) WITH FUNCTION missing_default_cast(cast_src);\n")...)
				if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(bad)); err == nil {
					t.Fatal("invalid late default replay replaced live catalog")
				}
				for _, s := range checks {
					run(s)
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
						t.Fatalf("actual routine-default SQL on PostgreSQL: %v\n%s", err, dump.String())
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
