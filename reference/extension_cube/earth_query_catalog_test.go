package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func earthQueryCatalogChecks() []fixture.Step {
	var steps []fixture.Step
	for _, tc := range []struct{ name, args, result string }{
		{"earth_distance", "earth,earth", "701"},
		{"earth_box", "earth,double precision", "'cube'::regtype::oid"},
	} {
		id := cubeLiteral("public."+tc.name+"("+tc.args+")") + "::regprocedure::oid"
		q := "SELECT proname AS name,pronargs::integer AS nargs,prorettype=" + tc.result + " AS result,proisstrict AS strict,provolatile::text AS volatility,proparallel::text AS parallel,prokind::text AS kind,proretset AS set_result,oid>0 AS identity,pronamespace='public'::regnamespace AND proowner=(SELECT extowner FROM pg_extension WHERE extname='earthdistance') AS authority FROM pg_proc WHERE oid=" + id
		steps = append(steps, fixture.Select(q, []string{"name", "nargs", "result", "strict", "volatility", "parallel", "kind", "set_result", "identity", "authority"}, []string{"NAME", "INT4", "BOOL", "BOOL", "TEXT", "TEXT", "TEXT", "BOOL", "BOOL", "BOOL"}, [][]any{{tc.name, int64(2), true, true, "i", "s", "f", false, true, true}}))
		steps = append(steps, fixture.Select("SELECT deptype FROM pg_depend WHERE classid=1255 AND objid="+id+" AND refclassid=3079 AND refobjid=(SELECT oid FROM pg_extension WHERE extname='earthdistance')", []string{"deptype"}, []string{"CHAR"}, [][]any{{"e"}}))
	}
	return steps
}

func TestReferenceEarthQueryCatalog(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "earth_query_catalog_moved")
	earthCoordinateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		earthInstall(t, p, b, c)
		for _, s := range earthQueryCatalogChecks() {
			earthQueryRoutes(t, p, b, c, s)
		}
		earthQueryRoutes(t, p, b, c, fixture.Select("SELECT count(*)::integer AS n FROM pg_depend WHERE refclassid=3079 AND refobjid=(SELECT oid FROM pg_extension WHERE extname='earthdistance') AND deptype='e'", []string{"n"}, []string{"INT4"}, [][]any{{int64(12)}}))
		for _, tc := range []struct {
			id      string
			helpers [][]any
		}{
			{"earth_distance(earth,earth)", [][]any{{"cube_distance"}, {"sec_to_gc"}}},
			{"earth_box(earth,double precision)", [][]any{{"cube_enlarge"}, {"gc_to_sec"}}},
		} {
			q := "SELECT p.proname FROM pg_depend d JOIN pg_proc p ON p.oid=d.refobjid WHERE d.classid=1255 AND d.objid=" + cubeLiteral(tc.id) + "::regprocedure::oid AND d.refclassid=1255 AND d.deptype='n' ORDER BY p.proname"
			earthQueryRoutes(t, p, b, c, fixture.Select(q, []string{"proname"}, []string{"NAME"}, tc.helpers))
		}
		// Identity, not historic names, owns the direct dependency after ALTER.
		for _, q := range []string{
			"CREATE SCHEMA earth_query_catalog_moved",
			"ALTER FUNCTION gc_to_sec(float8) RENAME TO earth_query_catalog_chord_now",
			"ALTER FUNCTION earth_query_catalog_chord_now(float8) SET SCHEMA earth_query_catalog_moved",
			"ALTER FUNCTION earth_box(earth,float8) RENAME TO earth_query_catalog_box_now",
			"ALTER FUNCTION earth_query_catalog_box_now(earth,float8) SET SCHEMA earth_query_catalog_moved",
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
		}
		q := "SELECT p.proname,p.pronamespace='earth_query_catalog_moved'::regnamespace AS moved FROM pg_depend d JOIN pg_proc p ON p.oid=d.refobjid WHERE d.classid=1255 AND d.objid='earth_query_catalog_moved.earth_query_catalog_box_now(earth,double precision)'::regprocedure::oid AND d.refclassid=1255 AND d.deptype='n' ORDER BY p.proname"
		earthQueryRoutes(t, p, b, c, fixture.Select(q, []string{"proname", "moved"}, []string{"NAME", "BOOL"}, [][]any{{"cube_enlarge", false}, {"earth_query_catalog_chord_now", true}}))
		earthQueryRoutes(t, p, b, c, cubeConstructorSelect(b, "SELECT earth_query_catalog_moved.earth_query_catalog_box_now('(6378168)'::earth,0) AS b", []string{"b"}, []string{"CUBE"}, [][]any{{"(6378168)"}}))
	})
}
