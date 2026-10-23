package reference_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceCastDefinitionAuthority(t *testing.T) {
	for _, tc := range []struct{ name, owner, grant, query, state, message string }{
		{"neither", "", "", "CREATE CAST (cast_src AS cast_other) WITH INOUT", "42501", "must be owner of type cast_src or type cast_other"},
		{"source_only", "ALTER TYPE cast_src OWNER TO cast_actor", "GRANT USAGE ON TYPE cast_other TO cast_actor", "CREATE CAST (cast_src AS cast_other) WITH INOUT", "", ""},
		{"target_only", "ALTER TYPE cast_other OWNER TO cast_actor", "GRANT USAGE ON TYPE cast_src TO cast_actor", "CREATE CAST (cast_src AS cast_other) WITH INOUT", "", ""},
		{"owner_membership", "ALTER TYPE cast_src OWNER TO cast_owner; GRANT cast_owner TO cast_actor", "GRANT USAGE ON TYPE cast_other TO cast_actor", "CREATE CAST (cast_src AS cast_other) WITH INOUT", "", ""},
		{"missing_usage", "ALTER TYPE cast_src OWNER TO cast_actor", "", "CREATE CAST (cast_src AS cast_other) WITH INOUT", "42501", "permission denied for type cast_other"},
		{"binary_superuser", "ALTER TYPE cast_src OWNER TO cast_actor", "GRANT USAGE ON TYPE cast_other TO cast_actor", "CREATE CAST (cast_src AS cast_other) WITHOUT FUNCTION", "42501", "must be superuser to create a cast WITHOUT FUNCTION"},
		{"create_no_execute", "ALTER TYPE cast_src OWNER TO cast_actor", "GRANT USAGE ON TYPE cast_other TO cast_actor", castDefinitionEdge, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"cast_actor", "cast_owner"})
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				conn, err := backend.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				defer conn.ExecContext(p.Ctx, "RESET ROLE")
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s) }
				// The fixture recreates public without the initdb PUBLIC USAGE
				// grant. SET ROLE otherwise hides its types from search_path.
				for _, q := range []string{"CREATE ROLE cast_actor", "CREATE ROLE cast_owner", "GRANT USAGE ON SCHEMA public TO cast_actor", castDefinitionSource, "CREATE TYPE cast_other AS ENUM ('red')", castDefinitionScore, "REVOKE ALL ON TYPE cast_src FROM PUBLIC", "REVOKE ALL ON TYPE cast_other FROM PUBLIC", "REVOKE ALL ON FUNCTION cast_score(cast_src) FROM PUBLIC"} {
					run(fixture.Exec(q, 0))
				}
				if tc.owner != "" {
					run(fixture.Exec(tc.owner, 0))
				}
				if tc.grant != "" {
					run(fixture.Exec(tc.grant, 0))
				}
				run(fixture.Exec("SET ROLE cast_actor", 0))
				if tc.state != "" {
					run(fixture.ErrorDiagnostic(tc.query, fixture.ControlsReferenceDiagnostic(tc.state, tc.message)))
				} else {
					run(fixture.Exec(tc.query, 0))
					if tc.name == "create_no_execute" {
						run(fixture.ErrorDiagnostic("SELECT ('red'::cast_src)::integer", fixture.ControlsReferenceDiagnostic("42501", "permission denied for function cast_score")))
						run(fixture.Exec("RESET ROLE", 0))
						run(fixture.Exec("GRANT EXECUTE ON FUNCTION cast_score(cast_src) TO cast_actor", 0))
						run(fixture.Exec("SET ROLE cast_actor", 0))
						run(fixture.Select("SELECT ('red'::cast_src)::integer AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(41)}}))
						run(fixture.Exec("RESET ROLE", 0))
						run(fixture.Exec("REVOKE EXECUTE ON FUNCTION cast_score(cast_src) FROM cast_actor", 0))
						run(fixture.Exec("SET ROLE cast_actor", 0))
						// DROP uses endpoint ownership, never routine EXECUTE.
						run(fixture.Exec("DROP CAST (cast_src AS integer)", 0))
					} else {
						run(fixture.Exec("DROP CAST (cast_src AS cast_other)", 0))
					}
				}
				run(fixture.Exec("RESET ROLE", 0))
				run(fixture.Select("SELECT count(*) AS n FROM pg_cast WHERE castsource='cast_src'::regtype", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
			}
		})
	}
}

func TestReferenceCastDefinitionAtomicity(t *testing.T) {
	for _, method := range []string{"function", "io"} {
		t.Run(method, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				conn, err := backend.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s) }
				for _, q := range []string{castDefinitionSource, "CREATE TABLE effects(v integer)", "INSERT INTO effects VALUES (10)", "CREATE SEQUENCE cast_calls"} {
					n := int64(0)
					if q == "INSERT INTO effects VALUES (10)" {
						n = 1
					}
					run(fixture.Exec(q, n))
				}
				if method == "function" {
					for _, q := range []string{`CREATE FUNCTION cast_effect(cast_src) RETURNS integer LANGUAGE SQL AS $$ INSERT INTO effects VALUES (1); SELECT CASE WHEN $1::text='blue' THEN 1/(length($1::text)-4) ELSE (SELECT count(*)::integer FROM effects) END $$`, "CREATE CAST (cast_src AS integer) WITH FUNCTION cast_effect(cast_src)", "CREATE TABLE cast_dest(v integer)"} {
						run(fixture.Exec(q, 0))
					}
					// CREATE and Prepare must not invoke a volatile cast routine.
					q := "SELECT ($1::cast_src)::integer AS v"
					stmt, err := conn.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					run(fixture.Select("SELECT count(*) AS n FROM effects", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
					run(fixture.Select("SELECT ('red'::cast_src)::integer AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(2)}}))
					for _, prepared := range []bool{false, true} {
						s := fixture.ErrorDiagnostic("INSERT INTO cast_dest SELECT v::integer FROM (VALUES ('red'::cast_src),('blue'::cast_src)) AS s(v)", fixture.ControlsReferenceDiagnostic("22012", "division by zero"))
						s.Diagnostic.Context = `SQL function "cast_effect" statement 2`
						s.Prepared = prepared
						s.ErrorStage = "execute"
						run(s)
						run(fixture.Select("SELECT count(*) AS n FROM effects", []string{"n"}, []string{"INT8"}, [][]any{{int64(2)}}))
					}
					// Actual source expression, not a direct call to the cast routine.
					run(fixture.Exec("DROP CAST (cast_src AS integer)", 0))
					run(fixture.Exec(`CREATE FUNCTION cast_source() RETURNS cast_src LANGUAGE SQL AS $$ SELECT nextval('cast_calls'); SELECT 'red'::cast_src $$`, 0))
					run(fixture.Exec(castDefinitionScore, 0))
					run(fixture.Exec(castDefinitionEdge, 0))
					run(fixture.Select("SELECT cast_source()::integer AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(41)}}))
					run(fixture.Select("SELECT last_value FROM cast_calls", []string{"last_value"}, []string{"INT8"}, [][]any{{int64(1)}}))
				} else {
					for _, q := range []string{"CREATE TYPE cast_other AS ENUM ('red')", "CREATE TABLE cast_dest(v cast_other)", "CREATE CAST (cast_src AS cast_other) WITH INOUT AS ASSIGNMENT"} {
						run(fixture.Exec(q, 0))
					}
					for _, prepared := range []bool{false, true} {
						s := fixture.ErrorDiagnostic("INSERT INTO cast_dest VALUES ('red'::cast_src),('blue'::cast_src)", fixture.ControlsReferenceDiagnostic("22P02", `invalid input value for enum cast_other: "blue"`))
						s.Prepared = prepared
						s.ErrorStage = "execute"
						run(s)
					}
				}
				run(fixture.Select("SELECT count(*) AS n FROM cast_dest", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
				if !backend.PG {
					castDefinitionCleanup(t, p, backend, [2]string{"xid", "real"})
					ctx, cancel := context.WithCancel(p.Ctx)
					cancel()
					result, err := conn.ExecContext(ctx, "CREATE CAST (xid AS real) WITHOUT FUNCTION")
					if result != nil || !errors.Is(err, context.Canceled) {
						t.Fatalf("canceled CREATE result=%v error=%v", result, err)
					}
					run(fixture.Select("SELECT count(*) AS n FROM pg_cast WHERE castsource='xid'::regtype AND casttarget='real'::regtype", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
				}
			}
		})
	}
}
