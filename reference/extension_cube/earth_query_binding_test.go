package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceEarthQueryBinding(t *testing.T) {
	t.Run("names_and_types", earthQueryBindingNames)
	t.Run("execute_privilege", earthQueryExecutePrivilege)
}

func earthQueryBindingNames(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"earth_query_bind_owner"})
	fixture.SchemaReferenceCleanup(t, "earth_query_bind_cube", "earth_query_bind_geo", "earth_query_bind_shadow", "earth_query_bind_moved")
	earthCoordinateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, q := range []string{
			"CREATE SCHEMA earth_query_bind_cube", "CREATE SCHEMA earth_query_bind_geo", "CREATE SCHEMA earth_query_bind_shadow", "CREATE SCHEMA earth_query_bind_moved",
			"CREATE ROLE earth_query_bind_owner", "GRANT USAGE,CREATE ON SCHEMA earth_query_bind_moved TO earth_query_bind_owner",
			"CREATE EXTENSION cube SCHEMA earth_query_bind_cube",
			"SET search_path=earth_query_bind_geo,earth_query_bind_cube,public",
			"CREATE EXTENSION earthdistance SCHEMA earth_query_bind_geo",
			"CREATE DOMAIN earth_query_bind_shadow.earth AS text",
			"CREATE FUNCTION earth_query_bind_shadow.earth_box(earth_query_bind_geo.earth,float8) RETURNS text LANGUAGE SQL RETURN 'shadow'::text",
			"CREATE VIEW public.earth_query_bound AS SELECT earth_distance('(6378168)'::earth,'(6378168)'::earth) AS d,earth_box('(6378168)'::earth,0) AS b",
		} {
			run(fixture.Exec(q, 0))
		}
		for _, radius := range []string{"0", "0::real", "0::numeric", "'0'", "NULL::float8"} {
			want := [][]any{{"(6378168)"}}
			if radius == "NULL::float8" {
				want = [][]any{{nil}}
			}
			earthQueryRoutes(t, p, b, c, cubeConstructorSelect(b, "SELECT earth_box('(6378168)',"+radius+") AS b", []string{"b"}, []string{"CUBE"}, want))
		}
		earthQueryRoutes(t, p, b, c, fixture.Select("SELECT earth_distance('(6378168)','(6378168,0,0)') AS d", []string{"d"}, []string{"FLOAT8"}, [][]any{{float64(0)}}))
		// Ordinary implicit cube-to-earth coercion remains checked; it does not
		// install an unchecked cube overload or bypass the nominal formals.
		earthQueryRoutes(t, p, b, c, fixture.Select("SELECT earth_distance('(6378168)'::cube,'(6378168)'::cube) AS d", []string{"d"}, []string{"FLOAT8"}, [][]any{{float64(0)}}))
		earthQueryRoutes(t, p, b, c, cubeConstructorSelect(b, "SELECT earth_box('(6378168)'::cube,0) AS b", []string{"b"}, []string{"CUBE"}, [][]any{{"(6378168)"}}))
		oldSQL := "SELECT earth_query_bind_geo.earth_box($1::earth_query_bind_geo.earth,$2::float8) AS b"
		h := earthHeld(t, p, c, oldSQL)
		defer h.Statement.Close()
		var before int64
		if err := c.QueryRowContext(p.Ctx, "SELECT 'earth_query_bind_geo.earth_box(earth_query_bind_geo.earth,float8)'::regprocedure::oid::bigint").Scan(&before); err != nil {
			t.Fatal(err)
		}
		run(fixture.Exec("SET search_path=earth_query_bind_shadow,pg_catalog", 0))
		earthQueryRoutes(t, p, b, c, fixture.Select("SELECT earth_box('(6378168)'::earth_query_bind_geo.earth,0) AS b", []string{"b"}, []string{"TEXT"}, [][]any{{"shadow"}}))
		earthQueryRoutes(t, p, b, c, cubeConstructorSelect(b, "SELECT earth_query_bind_geo.earth_box('(6378168)'::earth_query_bind_geo.earth,0) AS b", []string{"b"}, []string{"CUBE"}, [][]any{{"(6378168)"}}))
		for _, tc := range []struct{ q, signature string }{
			{"SELECT earth_distance(NULL,NULL)", "earth_distance(unknown, unknown)"},
			{"SELECT earth_query_bind_geo.earth_box('(6378168)'::earth_query_bind_shadow.earth,0)", "earth_query_bind_geo.earth_box(earth, integer)"},
			{"SELECT earth_query_bind_geo.earth_distance('(6378168)'::earth_query_bind_shadow.earth,NULL)", "earth_query_bind_geo.earth_distance(earth, unknown)"},
		} {
			for _, route := range []string{"direct", "fresh", "held"} {
				earthCoordinateError(t, p, b, c, tc.q, route, nil, earthCoordinateMissing(tc.signature, "8"), true, nil)
			}
		}
		for _, q := range []string{
			"ALTER FUNCTION earth_query_bind_geo.earth_distance(earth_query_bind_geo.earth,earth_query_bind_geo.earth) RENAME TO distance_now",
			"ALTER FUNCTION earth_query_bind_geo.earth_box(earth_query_bind_geo.earth,float8) SET SCHEMA earth_query_bind_moved",
			"ALTER FUNCTION earth_query_bind_moved.earth_box(earth_query_bind_geo.earth,float8) OWNER TO earth_query_bind_owner",
		} {
			run(fixture.Exec(q, 0))
		}
		earthQueryRoutes(t, p, b, c, cubeConstructorSelect(b, "SELECT d,b FROM public.earth_query_bound", []string{"d", "b"}, []string{"FLOAT8", "CUBE"}, [][]any{{float64(0), "(6378168)"}}))
		earthQueryRoutes(t, p, b, c, cubeConstructorSelect(b, "SELECT earth_query_bind_moved.earth_box('(6378168)'::earth_query_bind_geo.earth,0) AS b", []string{"b"}, []string{"CUBE"}, [][]any{{"(6378168)"}}))
		run(fixture.Select("SELECT oid::bigint AS id,proowner=(SELECT oid FROM pg_roles WHERE rolname='earth_query_bind_owner') AS owner FROM pg_proc WHERE oid='earth_query_bind_moved.earth_box(earth_query_bind_geo.earth,float8)'::regprocedure", []string{"id", "owner"}, []string{"INT8", "BOOL"}, [][]any{{before, true}}))
		rows, err := h.Statement.QueryContext(p.Ctx, "(6378168)", float64(0))
		if rows != nil {
			rows.Close()
		}
		if err == nil {
			t.Fatal("held old SQL survived query schema move")
		}
		want := earthCoordinateMissing("earth_query_bind_geo.earth_box(earth_query_bind_geo.earth, double precision)", "8")
		fixture.AssertDiagnostic(t, err, &want)
	})
}

func earthQueryExecutePrivilege(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"earth_query_reader"})
	earthCoordinateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		earthInstall(t, p, b, c)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, q := range []string{
			"CREATE ROLE earth_query_reader",
			"GRANT USAGE ON SCHEMA public TO earth_query_reader",
			"REVOKE EXECUTE ON FUNCTION earth_distance(earth,earth),earth_box(earth,float8) FROM PUBLIC",
		} {
			run(fixture.Exec(q, 0))
		}
		defer c.ExecContext(p.Ctx, "RESET ROLE")
		for _, tc := range []struct {
			q, name, signature string
			args               []any
			types              []string
			want               [][]any
		}{
			{"SELECT earth_distance($1::earth,$2::earth) AS v", "earth_distance", "earth_distance(earth,earth)", []any{"(6378168)", "(6378168)"}, []string{"FLOAT8"}, [][]any{{float64(0)}}},
			{"SELECT earth_box($1::earth,$2::float8) AS v", "earth_box", "earth_box(earth,float8)", []any{"(6378168)", float64(0)}, []string{"CUBE"}, [][]any{{"(6378168)"}}},
		} {
			run(fixture.Exec("SET ROLE earth_query_reader", 0))
			h := earthHeld(t, p, c, tc.q)
			defer h.Statement.Close()
			want := fixture.ControlsReferenceDiagnostic("42501", "permission denied for function "+tc.name)
			s := fixture.ErrorDiagnostic(tc.q, want, tc.args...)
			s.SelectRows, s.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
			fixture.RunStep(t, p.Ctx, h, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, h, b.Recorder, s)
			run(fixture.Exec("RESET ROLE", 0))
			run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+tc.signature+" TO earth_query_reader", 0))
			run(fixture.Exec("SET ROLE earth_query_reader", 0))
			fixture.RunStep(t, p.Ctx, h, b.Recorder, cubeConstructorSelect(b, tc.q, []string{"v"}, tc.types, tc.want, tc.args...))
			h.Statement.Close()
			run(fixture.Exec("RESET ROLE", 0))
		}
	})
}
