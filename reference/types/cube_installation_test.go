package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceCubeInstallation(t *testing.T) {
	t.Run("identity", func(t *testing.T) {
		cubeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			for _, prepared := range []bool{false, true} {
				for _, tc := range []struct {
					q, position string
					selectRows  bool
				}{
					{"CREATE TABLE absent(v cube)", "23", false}, {"SELECT '1'::cube", "13", true},
				} {
					want := fixture.ControlsReferenceDiagnostic("42704", `type "cube" does not exist`)
					want.Position = tc.position
					cubeError(t, p, b, c, tc.q, prepared, tc.selectRows, tc.selectRows, want)
				}
			}
			cubeInstall(t, p, b, c)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE cube_install(id integer,v cube)", 0))
			run(fixture.Exec("INSERT INTO cube_install VALUES (1,'(3,1),(1,4)')", 1))
			run(cubeSelect(b, "SELECT id,v FROM cube_install ORDER BY id", []string{"id", "v"}, [][]any{{int64(1), "(3, 1),(1, 4)"}}))
			run(fixture.Select("SELECT extname,extversion,extrelocatable FROM pg_extension WHERE extname='cube'", []string{"extname", "extversion", "extrelocatable"}, []string{"NAME", "TEXT", "BOOL"}, [][]any{{"cube", "1.5", true}}))
			var oid, qualified, text int64
			for _, tc := range []struct {
				q   string
				out *int64
			}{
				{"SELECT ('cube'::regtype)::oid", &oid}, {"SELECT ('public.cube'::regtype)::oid", &qualified}, {"SELECT ('text'::regtype)::oid", &text},
			} {
				if err := c.QueryRowContext(p.Ctx, tc.q).Scan(tc.out); err != nil {
					t.Fatal(err)
				}
			}
			if oid <= 0 || qualified != oid || text == oid {
				t.Fatalf("cube identities %d/%d text=%d", oid, qualified, text)
			}
			for _, prepared := range []bool{false, true} {
				want := fixture.ControlsReferenceDiagnostic("42710", `extension "cube" already exists`)
				cubeError(t, p, b, c, "CREATE EXTENSION cube", prepared, false, false, want)
			}
			var notices []fixture.Diagnostic
			_, noticeConn, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			fixture.RunStep(t, p.Ctx, noticeConn, recorder, fixture.Exec("CREATE EXTENSION IF NOT EXISTS cube", 0))
			want := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42710", Message: `extension "cube" already exists, skipping`}
			if len(notices) != 1 || notices[0] != want {
				t.Fatalf("notices %#v want %#v", notices, want)
			}
		})
	})
	t.Run("rollback", func(t *testing.T) {
		cubeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("BEGIN", 0))
			cubeInstall(t, p, b, c)
			run(cubeSelect(b, "SELECT $1::cube AS v", []string{"v"}, [][]any{{"(1, 2)"}}, "1,2"))
			run(fixture.Exec("ROLLBACK", 0))
			run(fixture.Select("SELECT extname FROM pg_extension WHERE extname='cube'", []string{"extname"}, []string{"NAME"}, [][]any{}))
			want := fixture.ControlsReferenceDiagnostic("42704", `type "cube" does not exist`)
			want.Position = "13"
			cubeError(t, p, b, c, "SELECT '1'::cube", false, true, false, want)
			cubeInstall(t, p, b, c)
			run(fixture.Exec("CREATE TABLE cube_after_rollback(v cube)", 0))
		})
	})
	t.Run("qualified", func(t *testing.T) {
		fixture.SchemaReferenceCleanup(t, "cube_install_namespace")
		cubeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE SCHEMA cube_install_namespace", 0))
			run(fixture.Exec("CREATE EXTENSION cube SCHEMA cube_install_namespace VERSION '1.5'", 0))
			run(fixture.Exec("CREATE TABLE public.cube_qualified(id integer,v cube_install_namespace.cube)", 0))
			run(fixture.Exec("SET search_path=cube_install_namespace,public", 0))
			run(fixture.Exec("CREATE TABLE public.cube_path(v cube)", 0))
			run(fixture.Exec("INSERT INTO public.cube_qualified VALUES (1,'(3,1),(1,4)'),(2,NULL)", 2))
			run(cubeSelect(b, "SELECT id,v FROM public.cube_qualified ORDER BY id", []string{"id", "v"}, [][]any{{int64(1), "(3, 1),(1, 4)"}, {int64(2), nil}}))
			run(fixture.Select("SELECT t.typname,n.nspname,p.proname AS input,o.proname AS output FROM pg_type t JOIN pg_namespace n ON n.oid=t.typnamespace JOIN pg_proc p ON p.oid=t.typinput JOIN pg_proc o ON o.oid=t.typoutput WHERE t.oid=('cube'::regtype)::oid", []string{"typname", "nspname", "input", "output"}, []string{"NAME", "NAME", "NAME", "NAME"}, [][]any{{"cube", "cube_install_namespace", "cube_in", "cube_out"}}))
		})
	})
	t.Run("conflicting_type", func(t *testing.T) {
		cubeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE TYPE cube AS ENUM ('occupied')", 0))
			want := fixture.ControlsReferenceDiagnostic("42710", `type "cube" already exists`)
			// Pinned PostgreSQL 18.6 exposes the released extension script Context.
			want.Context = "SQL statement \"CREATE TYPE cube (\n\tINTERNALLENGTH = variable,\n\tINPUT = cube_in,\n\tOUTPUT = cube_out,\n\tALIGNMENT = double\n)\"\nextension script file \"cube--1.2.sql\", near line 26"
			cubeError(t, p, b, c, "CREATE EXTENSION cube", false, false, false, want)
			run(fixture.Select("SELECT extname FROM pg_extension WHERE extname='cube'", []string{"extname"}, []string{"NAME"}, [][]any{}))
			run(fixture.Select("SELECT typname,typtype FROM pg_type WHERE typnamespace='public'::regnamespace AND typname IN ('cube','_cube') ORDER BY typname", []string{"typname", "typtype"}, []string{"NAME", "CHAR"}, [][]any{{"_cube", "b"}, {"cube", "e"}}))
			run(fixture.Select("SELECT proname FROM pg_proc WHERE pronamespace='public'::regnamespace AND proname IN ('cube_in','cube_out') ORDER BY proname", []string{"proname"}, []string{"NAME"}, [][]any{}))
			fixture.EnumReferenceRunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT 'occupied'::cube AS v", []string{"v"}, []string{""}, [][]any{{"occupied"}}))
		})
	})
}
