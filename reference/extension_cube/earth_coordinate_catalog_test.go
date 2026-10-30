package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceEarthCoordinateCatalog(t *testing.T) {
	earthCoordinateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		earthInstall(t, p, b, c)
		for _, tc := range []struct {
			name, args, result string
			n                  int64
			strict             bool
		}{
			{"earth", "", "701", 0, false},
			{"sec_to_gc", "double precision", "701", 1, true},
			{"gc_to_sec", "double precision", "701", 1, true},
			{"ll_to_earth", "double precision,double precision", "'earth'::regtype::oid", 2, true},
			{"latitude", "earth", "701", 1, true},
			{"longitude", "earth", "701", 1, true},
		} {
			// Exact regprocedure resolution names every formal, including the
			// installed earth domain; the selected pg_proc row below proves
			// arity/result/properties for that identity without proargtypes.
			id := cubeLiteral("public."+tc.name+"("+tc.args+")") + "::regprocedure::oid"
			q := "SELECT proname AS name,pronargs::integer AS nargs,prorettype=" + tc.result + " AS result,proisstrict AS strict,provolatile::text AS volatility,proparallel::text AS parallel,prokind::text AS kind,oid>0 AS identity,pronamespace='public'::regnamespace AND proowner=(SELECT extowner FROM pg_extension WHERE extname='earthdistance') AS authority FROM pg_proc WHERE oid=" + id
			earthCoordinateBoth(t, p, b, c, fixture.Select(q, []string{"name", "nargs", "result", "strict", "volatility", "parallel", "kind", "identity", "authority"}, []string{"NAME", "INT4", "BOOL", "BOOL", "TEXT", "TEXT", "TEXT", "BOOL", "BOOL"}, [][]any{{tc.name, tc.n, true, tc.strict, "i", "s", "f", true, true}}))
			earthCoordinateBoth(t, p, b, c, fixture.Select("SELECT deptype FROM pg_depend WHERE classid=1255 AND objid="+id+" AND refclassid=3079 AND refobjid=(SELECT oid FROM pg_extension WHERE extname='earthdistance')", []string{"deptype"}, []string{"CHAR"}, [][]any{{"e"}}))
		}
		earthCoordinateBoth(t, p, b, c, fixture.Select("SELECT count(*)::integer AS n,count(DISTINCT oid)::integer AS identities FROM pg_proc WHERE pronamespace='public'::regnamespace AND proname IN ('earth','sec_to_gc','gc_to_sec','ll_to_earth','latitude','longitude')", []string{"n", "identities"}, []string{"INT4", "INT4"}, [][]any{{int64(6), int64(6)}}))
		// Scope-filter the release's larger extension: the selected six routines
		// and its domain/companion are eight members, not its entire PG manifest.
		earthCoordinateBoth(t, p, b, c, fixture.Select("SELECT count(*)::integer AS n FROM pg_depend WHERE refclassid=3079 AND refobjid=(SELECT oid FROM pg_extension WHERE extname='earthdistance') AND deptype='e' AND ((classid=1255 AND objid IN ('earth()'::regprocedure::oid,'sec_to_gc(float8)'::regprocedure::oid,'gc_to_sec(float8)'::regprocedure::oid,'ll_to_earth(float8,float8)'::regprocedure::oid,'latitude(earth)'::regprocedure::oid,'longitude(earth)'::regprocedure::oid)) OR (classid=1247 AND objid IN ('earth'::regtype::oid,'earth[]'::regtype::oid)))", []string{"n"}, []string{"INT4"}, [][]any{{int64(8)}}))
		earthIdentityChecks(t, p, b, c, b.Recorder)
	})
}
