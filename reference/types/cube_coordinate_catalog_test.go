package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func cubeCoordinateCatalogChecks(schema, owner string) []fixture.Step {
	cube := cubeLiteral(schema+".cube") + "::regtype::oid"
	namespace := cubeLiteral(schema) + "::regnamespace::oid"
	ext := "(SELECT oid FROM pg_extension WHERE extname='cube')"
	ownerID := "(SELECT extowner FROM pg_extension WHERE extname='cube')"
	if owner != "" {
		ownerID = "(SELECT oid FROM pg_roles WHERE rolname=" + cubeLiteral(owner) + ")"
	}
	proc := func(name string) string {
		return cubeLiteral(schema+"."+name+"("+schema+".cube,integer)") + "::regprocedure::oid"
	}
	var checks []fixture.Step
	for _, name := range []string{"cube_ll_coord", "cube_ur_coord", "cube_coord", "cube_coord_llur"} {
		id := proc(name)
		q := "SELECT proname AS name,pronargs::integer AS nargs,prorettype::integer AS result,proisstrict AS strict,provolatile::text AS volatility,proparallel::text AS parallel,prokind::text AS kind,oid>0 AS identity,(pronamespace=" + namespace + " AND proowner=" + ownerID + ") AS authority FROM pg_proc WHERE oid=" + id
		checks = append(checks, fixture.Select(q, []string{"name", "nargs", "result", "strict", "volatility", "parallel", "kind", "identity", "authority"}, []string{"NAME", "INT4", "INT4", "BOOL", "TEXT", "TEXT", "TEXT", "BOOL", "BOOL"}, [][]any{{name, int64(2), int64(701), true, "i", "s", "f", true, true}}))
		checks = append(checks, fixture.Select("SELECT deptype FROM pg_depend WHERE classid=1255 AND objid="+id+" AND refclassid=3079 AND refobjid="+ext, []string{"deptype"}, []string{"CHAR"}, [][]any{{"e"}}))
	}
	for _, member := range []struct{ op, procedure string }{{"->", "cube_coord"}, {"~>", "cube_coord_llur"}} {
		id := cubeLiteral(schema+"."+member.op+"("+schema+".cube,integer)") + "::regoperator::oid"
		q := "SELECT oprname AS name,oprresult::integer AS result,oprcode::oid=" + proc(member.procedure) + " AS procedure,(oprleft=" + cube + " AND oprright=23) AS signature,oid>0 AS identity,(oprnamespace=" + namespace + " AND oprowner=" + ownerID + ") AS authority,oprcom::integer,oprnegate::integer,oprcanhash,oprcanmerge,oprrest::oid::integer,oprjoin::oid::integer FROM pg_operator WHERE oid=" + id
		checks = append(checks, fixture.Select(q, []string{"name", "result", "procedure", "signature", "identity", "authority", "oprcom", "oprnegate", "oprcanhash", "oprcanmerge", "oprrest", "oprjoin"}, []string{"NAME", "INT4", "BOOL", "BOOL", "BOOL", "BOOL", "INT4", "INT4", "BOOL", "BOOL", "INT4", "INT4"}, [][]any{{member.op, int64(701), true, true, true, true, int64(0), int64(0), false, false, int64(0), int64(0)}}))
		checks = append(checks, fixture.Select("SELECT deptype FROM pg_depend WHERE classid=2617 AND objid="+id+" AND refclassid=3079 AND refobjid="+ext, []string{"deptype"}, []string{"CHAR"}, [][]any{{"e"}}))
	}
	checks = append(checks, fixture.Select("SELECT "+proc("cube_ll_coord")+"<>"+proc("cube_ur_coord")+" AND "+proc("cube_coord")+"<>"+proc("cube_coord_llur")+" AS distinct_identity", []string{"distinct_identity"}, []string{"BOOL"}, [][]any{{true}}))
	return checks
}

func TestReferenceCubeCoordinateCatalog(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "cube_coordinate_provider")
	cubeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE SCHEMA cube_coordinate_provider", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE EXTENSION cube SCHEMA cube_coordinate_provider", 0))
		for _, s := range cubeCoordinateCatalogChecks("cube_coordinate_provider", "") {
			cubeSpatialBoth(t, p, b, c, s)
		}
		q := "SELECT cube_coordinate_provider.cube_ll_coord('(3,1),(1,4)'::cube_coordinate_provider.cube,1) AS lower,cube_coordinate_provider.cube_ur_coord('(3,1),(1,4)'::cube_coordinate_provider.cube,1) AS upper,'(3,1),(1,4)'::cube_coordinate_provider.cube OPERATOR(cube_coordinate_provider.->) 1 AS raw,'(3,1),(1,4)'::cube_coordinate_provider.cube OPERATOR(cube_coordinate_provider.~>) 1 AS projection"
		cubeSpatialBoth(t, p, b, c, cubeCoordinateSelect(b, q, [][]any{{float64(1), float64(3), float64(3), float64(1)}}))
	})
}
