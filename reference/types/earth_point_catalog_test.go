package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func earthPointCatalogChecks(schema, name, owner string) []fixture.Step {
	proc := cubeLiteral(schema+"."+name+"(point,point)") + "::regprocedure::oid"
	op := "'public.<@>(point,point)'::regoperator::oid"
	ext := "(SELECT oid FROM pg_extension WHERE extname='earthdistance')"
	ownerID := "(SELECT extowner FROM pg_extension WHERE extname='earthdistance')"
	if owner != "" {
		ownerID = "(SELECT oid FROM pg_roles WHERE rolname=" + cubeLiteral(owner) + ")"
	}
	return []fixture.Step{
		fixture.Select("SELECT proname,pronargs::integer AS nargs,prorettype::integer AS result,proisstrict,provolatile::text AS volatility,proparallel::text AS parallel,prokind::text AS kind,oid>0 AS identity,pronamespace="+cubeLiteral(schema)+"::regnamespace AND proowner="+ownerID+" AS authority FROM pg_proc WHERE oid="+proc,
			[]string{"proname", "nargs", "result", "proisstrict", "volatility", "parallel", "kind", "identity", "authority"}, []string{"NAME", "INT4", "INT4", "BOOL", "TEXT", "TEXT", "TEXT", "BOOL", "BOOL"}, [][]any{{name, int64(2), int64(701), true, "i", "s", "f", true, true}}),
		fixture.Select("SELECT oprname,oprleft::integer AS left_type,oprright::integer AS right_type,oprresult::integer AS result,oprcode::oid="+proc+" AS backing,oprcom=oid AS commutator,oid>0 AS identity,oprnamespace='public'::regnamespace AS namespace,oprnegate::integer,oprcanhash,oprcanmerge,oprrest::oid::integer,oprjoin::oid::integer FROM pg_operator WHERE oid="+op,
			[]string{"oprname", "left_type", "right_type", "result", "backing", "commutator", "identity", "namespace", "oprnegate", "oprcanhash", "oprcanmerge", "oprrest", "oprjoin"}, []string{"NAME", "INT4", "INT4", "INT4", "BOOL", "BOOL", "BOOL", "BOOL", "INT4", "BOOL", "BOOL", "INT4", "INT4"}, [][]any{{"<@>", int64(600), int64(600), int64(701), true, true, true, true, int64(0), false, false, int64(0), int64(0)}}),
		fixture.Select("SELECT deptype FROM pg_depend WHERE classid=1255 AND objid="+proc+" AND refclassid=3079 AND refobjid="+ext, []string{"deptype"}, []string{"CHAR"}, [][]any{{"e"}}),
		fixture.Select("SELECT deptype FROM pg_depend WHERE classid=2617 AND objid="+op+" AND refclassid=3079 AND refobjid="+ext, []string{"deptype"}, []string{"CHAR"}, [][]any{{"e"}}),
	}
}

func TestReferenceEarthPointCatalog(t *testing.T) {
	t.Run("installation", func(t *testing.T) {
		earthPointScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			absent := func() {
				run(fixture.Select("SELECT proname FROM pg_proc WHERE pronamespace='public'::regnamespace AND proname='geo_distance'", []string{"proname"}, []string{"NAME"}, [][]any{}))
				run(fixture.Select("SELECT oprname FROM pg_operator WHERE oprnamespace='public'::regnamespace AND oprname='<@>' AND oprleft=600 AND oprright=600", []string{"oprname"}, []string{"NAME"}, [][]any{}))
			}
			absent()
			cubeInstall(t, p, b, c)
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("CREATE EXTENSION earthdistance", 0))
			for _, s := range earthPointCatalogChecks("public", "geo_distance", "") {
				earthCoordinateBoth(t, p, b, c, s)
			}
			run(fixture.Exec("ROLLBACK", 0))
			absent()
			earthAbsent(t, p, b, c)
			run(fixture.Exec("CREATE EXTENSION earthdistance", 0))
			for _, s := range earthPointCatalogChecks("public", "geo_distance", "") {
				earthCoordinateBoth(t, p, b, c, s)
			}
		})
	})
	t.Run("procedure_conflict", func(t *testing.T) {
		earthPointScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			cubeInstall(t, p, b, c)
			run(fixture.Exec("CREATE FUNCTION geo_distance(point,point) RETURNS float8 LANGUAGE SQL RETURN 7::float8", 0))
			var id int64
			if err := c.QueryRowContext(p.Ctx, "SELECT 'geo_distance(point,point)'::regprocedure::oid::bigint").Scan(&id); err != nil {
				t.Fatal(err)
			}
			want := fixture.ControlsReferenceDiagnostic("42723", "function \"geo_distance\" already exists with same argument types")
			want.Context = "SQL statement \"CREATE FUNCTION geo_distance (point, point)\nRETURNS float8\nLANGUAGE C IMMUTABLE STRICT PARALLEL SAFE AS '$libdir/earthdistance'\"\nextension script file \"earthdistance--1.1.sql\", near line 87"
			run(fixture.ErrorDiagnostic("CREATE EXTENSION earthdistance", want))
			earthAbsent(t, p, b, c)
			run(fixture.Select("SELECT oid::bigint AS identity FROM pg_proc WHERE oid='geo_distance(point,point)'::regprocedure", []string{"identity"}, []string{"INT8"}, [][]any{{id}}))
			earthCoordinateBoth(t, p, b, c, fixture.Select("SELECT geo_distance(point '(0,0)',point '(1,0)') AS sentinel", []string{"sentinel"}, []string{"FLOAT8"}, [][]any{{float64(7)}}))
			run(fixture.Select("SELECT oprname FROM pg_operator WHERE oprnamespace='public'::regnamespace AND oprname='<@>'", []string{"oprname"}, []string{"NAME"}, [][]any{}))
		})
	})
}
