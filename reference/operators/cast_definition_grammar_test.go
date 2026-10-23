package reference_test

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceCastDefinitionGrammar(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "cast_names")
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			conn, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s) }
			castDefinitionCleanup(t, p, backend, [2]string{"xid[]", "real[]"}, [2]string{"xid", "real"})
			for _, q := range []string{"CREATE SCHEMA cast_names", `CREATE TYPE cast_names."Source" AS ENUM ('red')`, `CREATE FUNCTION cast_names."Convert"(cast_names."Source") RETURNS integer LANGUAGE SQL AS $$ SELECT 51 $$`, `CREATE CAST (cast_names."Source" AS int4) WITH FUNCTION cast_names."Convert"(cast_names."Source") AS IMPLICIT`} {
				run(fixture.Prepared(fixture.Exec(q, 0)))
			}
			run(fixture.Select(`SELECT ('red'::cast_names."Source")::integer AS v`, []string{"v"}, []string{"INT4"}, [][]any{{int64(51)}}))
			run(fixture.Prepared(fixture.Exec(`DROP CAST (cast_names."Source" AS integer) RESTRICT`, 0)))
			// Qualified function lookup must not accidentally bind an empty signature.
			run(fixture.Exec(`CREATE CAST (cast_names."Source" AS integer) WITH FUNCTION cast_names."Convert"`, 0))
			run(fixture.Exec(`DROP CAST (cast_names."Source" AS integer) CASCADE`, 0))
			for _, tc := range []struct {
				query, token string
				position     int
			}{
				{`CREATE OR REPLACE CAST (cast_names."Source" AS integer) WITH INOUT`, "CAST", 19},
				{`CREATE CAST IF NOT EXISTS (cast_names."Source" AS integer) WITH INOUT`, "IF", 13},
				{`CREATE CAST (cast_names."Source" AS integer) WITH INOUT garbage`, "garbage", 57},
			} {
				d := fixture.ControlsReferenceDiagnostic("42601", `syntax error at or near "`+tc.token+`"`)
				d.Position = fixture.Position(tc.position)
				run(fixture.ErrorDiagnostic(tc.query, d))
			}
			run(fixture.Select(`SELECT count(*) AS n FROM pg_cast WHERE castsource='cast_names."Source"'::regtype`, []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
			// Array type syntax is accepted by FUNCTION/INOUT, but not binary form.
			run(fixture.Exec("CREATE CAST (xid[] AS real[]) WITH INOUT", 0))
			d := fixture.ControlsReferenceDiagnostic("42846", "cannot cast type xid to real")
			d.Position = "15"
			run(fixture.ErrorDiagnostic("SELECT (ARRAY['42'::xid]::real[])[1] AS v", d))
			run(fixture.Exec("CREATE CAST (xid AS real) WITH INOUT", 0))
			run(fixture.Select("SELECT (ARRAY['42'::xid]::real[])[1] AS v", []string{"v"}, []string{"FLOAT4"}, [][]any{{float64(42)}}))
			run(fixture.Exec("DROP CAST IF EXISTS (xid[] AS real[])", 0))
		})
	}
}

func TestReferenceCastDefinitionNotices(t *testing.T) {
	for _, tc := range []struct{ name, setup, query, state, message string }{
		{"missing_cast", castDefinitionSource, "DROP CAST IF EXISTS (cast_src AS integer) RESTRICT", "00000", "cast from type cast_src to type pg_catalog.int4 does not exist, skipping"},
		{"missing_type", "", "DROP CAST IF EXISTS (cast_missing AS integer)", "00000", `type "cast_missing" does not exist, skipping`},
		{"missing_schema", "", "DROP CAST IF EXISTS (cast_missing.type AS integer)", "00000", `schema "cast_missing" does not exist, skipping`},
		{"source_domain", "CREATE DOMAIN cast_domain AS integer; CREATE DOMAIN cast_target AS integer", "CREATE CAST (cast_domain AS cast_target) WITH INOUT", "42809", "cast will be ignored because the source data type is a domain"},
		{"target_domain", castDefinitionSource + "; CREATE DOMAIN cast_domain AS integer", "CREATE CAST (cast_src AS cast_domain) WITH INOUT", "42809", "cast will be ignored because the target data type is a domain"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				var notices []fixture.Diagnostic
				_, conn, rec := fixture.NoticeReferencePool(t, p, backend.PG, &notices)
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, rec, s) }
				if tc.setup != "" {
					run(fixture.Exec(tc.setup, 0))
				}
				notices = nil
				run(fixture.Exec(tc.query, 0))
				severity := "NOTICE"
				if tc.state == "42809" {
					severity = "WARNING"
				}
				want := []fixture.Diagnostic{{Severity: severity, SeverityUnlocalized: severity, Code: tc.state, Message: tc.message}}
				if !reflect.DeepEqual(notices, want) {
					t.Fatalf("cast notice complete fields got %#v want %#v", notices, want)
				}
			}
		})
	}
}

func TestReferenceCastDefinitionSignatureAdapters(t *testing.T) {
	for _, tc := range []struct{ name, source, target, signature, state, message string }{
		{"aggregate", "integer", "bigint", "sum(integer)", "42P17", "cast function must be a normal function"},
		{"set", "integer", "integer", "generate_series(integer,integer)", "42P17", "cast function must not return a set"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.ErrorDiagnostic("CREATE CAST ("+tc.source+" AS "+tc.target+") WITH FUNCTION "+tc.signature, fixture.ControlsReferenceDiagnostic(tc.state, tc.message)))
			}
		})
	}
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
		castDefinitionCleanup(t, p, backend, [2]string{"text", "bytea"}, [2]string{"text", "integer"})
		for _, q := range []string{castDefinitionSource,
			`CREATE FUNCTION cast_two(cast_src,integer) RETURNS varchar LANGUAGE SQL AS $$ SELECT $2::text $$`,
			"CREATE CAST (cast_src AS varchar) WITH FUNCTION cast_two(cast_src,integer)",
			"CREATE CAST (text AS bytea) WITHOUT FUNCTION AS IMPLICIT",
			"CREATE FUNCTION bytes_input(bytea) RETURNS integer LANGUAGE SQL AS $$ SELECT length($1) $$",
			"CREATE CAST (text AS integer) WITH FUNCTION bytes_input(bytea)"} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Select("SELECT ('red'::cast_src)::varchar AS plain,('red'::cast_src)::varchar(5) AS sized", []string{"plain", "sized"}, []string{"VARCHAR", "VARCHAR"}, [][]any{{"-1", "9"}}))
		run(fixture.Select("SELECT ('abc'::text)::integer AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(3)}}))
	}
}

func TestReferenceCastDefinitionCancellation(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			conn, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s) }
			for _, q := range []string{castDefinitionSource, "CREATE TABLE cast_effects(v integer)", `CREATE FUNCTION cast_wait(cast_src) RETURNS integer LANGUAGE SQL AS $$ INSERT INTO cast_effects VALUES (1); SELECT pg_sleep(600); SELECT 41 $$`, "CREATE CAST (cast_src AS integer) WITH FUNCTION cast_wait(cast_src)"} {
				run(fixture.Exec(q, 0))
			}
			query := "SELECT ('red'::cast_src)::integer"
			fixture.SleepActiveCancelWithContext(t, p, backend, conn, query, `SQL function "cast_wait" statement 2`, nil, func(ctx context.Context) fixture.RelationOutcome {
				rows, err := conn.QueryContext(ctx, query)
				return fixture.RelationOutcome{Rows: rows, Err: err}
			})
			run(fixture.Select("SELECT count(*) AS n FROM cast_effects", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
			run(fixture.Select("SELECT count(*) AS n FROM pg_cast WHERE castsource='cast_src'::regtype", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
		})
	}
}
