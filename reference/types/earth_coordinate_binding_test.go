package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceEarthCoordinateBinding(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"earth_coordinate_owner"})
	fixture.SchemaReferenceCleanup(t, "earth_coordinate_cube", "earth_coordinate_geo", "earth_coordinate_shadow", "earth_coordinate_moved")
	earthCoordinateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, q := range []string{
			"CREATE SCHEMA earth_coordinate_cube", "CREATE SCHEMA earth_coordinate_geo", "CREATE SCHEMA earth_coordinate_shadow", "CREATE SCHEMA earth_coordinate_moved",
			"CREATE ROLE earth_coordinate_owner", "GRANT USAGE,CREATE ON SCHEMA earth_coordinate_geo TO earth_coordinate_owner",
			"CREATE EXTENSION cube SCHEMA earth_coordinate_cube",
			"SET search_path=earth_coordinate_geo,earth_coordinate_cube,public",
			"CREATE EXTENSION earthdistance SCHEMA earth_coordinate_geo",
			"CREATE DOMAIN earth_coordinate_shadow.earth AS text",
			"CREATE FUNCTION earth_coordinate_shadow.earth() RETURNS float8 LANGUAGE SQL RETURN 7::float8",
			"CREATE VIEW public.earth_coordinate_bound AS SELECT earth() AS r,latitude(ll_to_earth(45,90)) AS lat",
		} {
			run(fixture.Exec(q, 0))
		}
		for _, args := range []string{"45,90", "45::real,90::real", "45::numeric,90::numeric", "'45','90'"} {
			earthCoordinateNumbers(t, p, b, c, "SELECT latitude(ll_to_earth("+args+")) AS lat,longitude(ll_to_earth("+args+")) AS lon", []string{"lat", "lon"}, [][]any{{float64(45), float64(90)}}, []float64{1e-10, 1e-10})
		}
		var before int64
		if err := c.QueryRowContext(p.Ctx, "SELECT 'earth_coordinate_geo.earth()'::regprocedure::oid::bigint").Scan(&before); err != nil {
			t.Fatal(err)
		}
		h := earthHeld(t, p, c, "SELECT earth_coordinate_geo.latitude(earth_coordinate_geo.ll_to_earth($1::float8,$2::float8)) AS lat")
		defer h.Statement.Close()
		run(fixture.Exec("SET search_path=earth_coordinate_shadow,pg_catalog", 0))
		earthCoordinateBoth(t, p, b, c, fixture.Select("SELECT earth() AS r,earth_coordinate_geo.earth() AS authentic", []string{"r", "authentic"}, []string{"FLOAT8", "FLOAT8"}, [][]any{{float64(7), float64(6378168)}}))
		for _, route := range []string{"direct", "fresh", "held"} {
			earthCoordinateError(t, p, b, c, "SELECT earth_coordinate_geo.latitude('(6378168)'::earth_coordinate_shadow.earth)", route, nil, earthCoordinateMissing("earth_coordinate_geo.latitude(earth)", "8"), true, nil)
			earthCoordinateError(t, p, b, c, "SELECT ll_to_earth(0,0)", route, nil, earthCoordinateMissing("ll_to_earth(integer, integer)", "8"), true, nil)
		}
		for _, q := range []string{
			"ALTER FUNCTION earth_coordinate_geo.earth() RENAME TO radius_now",
			"ALTER FUNCTION earth_coordinate_geo.ll_to_earth(float8,float8) SET SCHEMA earth_coordinate_moved",
			"ALTER FUNCTION earth_coordinate_geo.radius_now() OWNER TO earth_coordinate_owner",
		} {
			run(fixture.Exec(q, 0))
		}
		earthCoordinateBoth(t, p, b, c, fixture.Select("SELECT earth_coordinate_geo.radius_now() AS r", []string{"r"}, []string{"FLOAT8"}, [][]any{{float64(6378168)}}))
		earthCoordinateNumbers(t, p, b, c, "SELECT r,lat FROM public.earth_coordinate_bound", []string{"r", "lat"}, [][]any{{float64(6378168), float64(45)}}, []float64{0, 1e-10})
		run(fixture.Select("SELECT oid::bigint AS id,proowner=(SELECT oid FROM pg_roles WHERE rolname='earth_coordinate_owner') AS owner FROM pg_proc WHERE oid='earth_coordinate_geo.radius_now()'::regprocedure", []string{"id", "owner"}, []string{"INT8", "BOOL"}, [][]any{{before, true}}))
		earthCoordinateBoth(t, p, b, c, fixture.Select("SELECT earth_coordinate_geo.latitude(earth_coordinate_moved.ll_to_earth(0,0)) AS lat", []string{"lat"}, []string{"FLOAT8"}, [][]any{{float64(0)}}))
		// Protocol plans reanalyze the old SQL after provider identity DDL;
		// the stored view above instead retains its checked hard capture.
		want := earthCoordinateMissing("earth_coordinate_geo.ll_to_earth(double precision, double precision)", "38")
		var lat float64
		err := h.Statement.QueryRowContext(p.Ctx, 0, 0).Scan(&lat)
		if err == nil {
			t.Fatal("held old SQL unexpectedly survived schema move")
		}
		fixture.AssertDiagnostic(t, err, &want)
		for _, route := range []string{"direct", "fresh", "held"} {
			earthCoordinateError(t, p, b, c, "SELECT earth_coordinate_geo.earth()", route, nil, earthCoordinateMissing("earth_coordinate_geo.earth()", "8"), true, nil)
		}
	})
}
