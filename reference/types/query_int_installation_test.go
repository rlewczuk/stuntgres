package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceQueryIntInstallation(t *testing.T) {
	t.Run("rollback", func(t *testing.T) {
		queryIntScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("BEGIN", 0))
			queryIntInstall(t, p, b, c)
			literal := queryIntSelect(b, "SELECT '1&(2|3)'::query_int AS v", []string{"v"}, [][]any{{"1 & ( 2 | 3 )"}})
			run(literal)
			run(fixture.Prepared(literal))
			bound := queryIntSelect(b, "SELECT $1::query_int AS v", []string{"v"}, [][]any{{"!( !1 )"}}, "!!1")
			run(bound)
			run(fixture.Prepared(bound))
			run(fixture.Exec("ROLLBACK", 0))
			run(fixture.Select("SELECT extname FROM pg_extension WHERE extname='intarray'", []string{"extname"}, []string{"NAME"}, [][]any{}))
			for _, prepared := range []bool{false, true} {
				want := fixture.ControlsReferenceDiagnostic("42704", `type "query_int" does not exist`)
				want.Position = "13"
				queryIntError(t, p, b, c, "SELECT '1'::query_int AS v", prepared, true, true, want)
			}
			queryIntInstall(t, p, b, c)
			run(fixture.Exec("CREATE TABLE qi_rollback(v query_int)", 0))
		})
	})
	t.Run("identity", func(t *testing.T) {
		queryIntScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			for _, prepared := range []bool{false, true} {
				for _, tc := range []struct {
					q, position string
					selectRows  bool
				}{
					{"CREATE TABLE absent(v query_int)", "23", false},
					{"SELECT '1'::query_int", "13", true},
				} {
					want := fixture.ControlsReferenceDiagnostic("42704", `type "query_int" does not exist`)
					want.Position = tc.position
					queryIntError(t, p, b, c, tc.q, prepared, tc.selectRows, tc.selectRows, want)
				}
			}
			queryIntInstall(t, p, b, c)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE qi_install(id integer,v query_int)", 0))
			run(fixture.Exec("INSERT INTO qi_install VALUES (1,'1&(2|3)')", 1))
			run(queryIntSelect(b, "SELECT id,v FROM qi_install ORDER BY id", []string{"id", "v"}, [][]any{{int64(1), "1 & ( 2 | 3 )"}}))
			run(fixture.Select("SELECT extname,extversion,extrelocatable FROM pg_extension WHERE extname='intarray'", []string{"extname", "extversion", "extrelocatable"}, []string{"NAME", "TEXT", "BOOL"}, [][]any{{"intarray", "1.5", true}}))
			var oid, repeated int64
			if err := c.QueryRowContext(p.Ctx, "SELECT ('query_int'::regtype)::oid").Scan(&oid); err != nil {
				t.Fatal(err)
			}
			if err := c.QueryRowContext(p.Ctx, "SELECT ('public.query_int'::regtype)::oid").Scan(&repeated); err != nil {
				t.Fatal(err)
			}
			if oid <= 0 || repeated != oid {
				t.Fatalf("query_int identity %d/%d", oid, repeated)
			}
			for _, prepared := range []bool{false, true} {
				want := fixture.ControlsReferenceDiagnostic("42710", `extension "intarray" already exists`)
				queryIntError(t, p, b, c, "CREATE EXTENSION intarray", prepared, false, false, want)
			}
			var notices []fixture.Diagnostic
			_, noticeConn, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			fixture.RunStep(t, p.Ctx, noticeConn, recorder, fixture.Exec("CREATE EXTENSION IF NOT EXISTS intarray", 0))
			wantNotice := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42710", Message: `extension "intarray" already exists, skipping`}
			if len(notices) != 1 || notices[0] != wantNotice {
				t.Fatalf("IF NOT EXISTS notices %#v want %#v", notices, wantNotice)
			}
		})
	})
	t.Run("qualified", func(t *testing.T) {
		fixture.SchemaReferenceCleanup(t, "qi_install_namespace")
		queryIntScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE SCHEMA qi_install_namespace", 0))
			run(fixture.Exec("CREATE EXTENSION intarray SCHEMA qi_install_namespace VERSION '1.5'", 0))
			run(fixture.Exec("CREATE TABLE qi_qualified(v qi_install_namespace.query_int)", 0))
			run(fixture.Exec("SET search_path=qi_install_namespace,public", 0))
			run(fixture.Exec("CREATE TABLE public.qi_path(v query_int)", 0))
			run(fixture.Exec("INSERT INTO public.qi_path VALUES ('!1|2'),(NULL)", 2))
			s := queryIntSelect(b, "SELECT v FROM public.qi_path", []string{"v"}, [][]any{{"!1 | 2"}, {nil}})
			s.Bag = true
			run(s)
			if b.PG {
				run(fixture.Select("SELECT t.typname,n.nspname,t.typtype,t.typlen,p.proname AS input,o.proname AS output FROM pg_type t JOIN pg_namespace n ON n.oid=t.typnamespace JOIN pg_proc p ON p.oid=t.typinput JOIN pg_proc o ON o.oid=t.typoutput WHERE t.oid=('qi_install_namespace.query_int'::regtype)::oid", []string{"typname", "nspname", "typtype", "typlen", "input", "output"}, []string{"NAME", "NAME", "CHAR", "INT2", "NAME", "NAME"}, [][]any{{"query_int", "qi_install_namespace", "b", int64(-1), "bqarr_in", "bqarr_out"}}))
			}
		})
	})
	for _, conflict := range []struct{ name, setup string }{
		{"type", "CREATE TYPE query_int AS ENUM ('occupied')"},
		{"input_helper", "CREATE FUNCTION bqarr_in(cstring) RETURNS text LANGUAGE internal IMMUTABLE STRICT AS 'textin'"},
	} {
		t.Run("conflict_"+conflict.name, func(t *testing.T) {
			queryIntScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				// LANGUAGE internal is a PostgreSQL native-ABI mechanism, not an
				// authorized stuntgres SQL loader. Native signature collisions are
				// covered by genuine provider identity units, not a fake SQL helper.
				if !b.PG && conflict.name == "input_helper" {
					return
				}
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				run(fixture.Exec(conflict.setup, 0))
				want := fixture.ControlsReferenceDiagnostic("42710", `type "query_int" already exists`)
				want.Context = "SQL statement \"CREATE TYPE query_int (\n\tINTERNALLENGTH = -1,\n\tINPUT = bqarr_in,\n\tOUTPUT = bqarr_out\n)\"\nextension script file \"intarray--1.2.sql\", near line 21"
				if conflict.name == "input_helper" {
					want = fixture.ControlsReferenceDiagnostic("42723", `function "bqarr_in" already exists with same argument types`)
					want.Context = "SQL statement \"CREATE FUNCTION bqarr_in(cstring)\nRETURNS query_int\nAS '$libdir/_int'\nLANGUAGE C STRICT IMMUTABLE PARALLEL SAFE\"\nextension script file \"intarray--1.2.sql\", near line 11"
				}
				queryIntError(t, p, b, c, "CREATE EXTENSION intarray", false, false, false, want)
				run(fixture.Select("SELECT extname FROM pg_extension WHERE extname='intarray'", []string{"extname"}, []string{"NAME"}, [][]any{}))
				if b.PG {
					typeRows, helperRows := [][]any{{"_query_int", "b"}, {"query_int", "e"}}, [][]any{}
					if conflict.name == "input_helper" {
						typeRows, helperRows = [][]any{}, [][]any{{"bqarr_in"}}
					}
					run(fixture.Select("SELECT typname,typtype FROM pg_type WHERE typnamespace='public'::regnamespace AND typname IN ('query_int','_query_int') ORDER BY typname", []string{"typname", "typtype"}, []string{"NAME", "CHAR"}, typeRows))
					run(fixture.Select("SELECT proname FROM pg_proc WHERE pronamespace='public'::regnamespace AND proname IN ('bqarr_in','bqarr_out') ORDER BY proname", []string{"proname"}, []string{"NAME"}, helperRows))
				} else {
					fixture.EnumReferenceRunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT 'occupied'::query_int AS v", []string{"v"}, []string{""}, [][]any{{"occupied"}}))
				}
			})
		})
	}
	t.Run("temporary", func(t *testing.T) {
		queryIntScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TEMP TABLE qi_temp_anchor(id integer)", 0))
			want := fixture.ControlsReferenceDiagnostic("3F000", `schema "pg_temp" does not exist`)
			queryIntError(t, p, b, c, "CREATE EXTENSION intarray SCHEMA pg_temp", false, false, false, want)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT extname FROM pg_extension WHERE extname='intarray'", []string{"extname"}, []string{"NAME"}, [][]any{}))
			want = fixture.ControlsReferenceDiagnostic("42704", `type "pg_temp.query_int" does not exist`)
			want.Position = "35"
			queryIntError(t, p, b, c, "CREATE TEMP TABLE qi_temp_value(v pg_temp.query_int)", false, false, false, want)
		})
	})
}
