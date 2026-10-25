package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func cubeSpatialCatalogChecks(schema string) []fixture.Step {
	// Each exact regprocedure/regoperator spelling independently identifies both
	// actual cube arguments. Single-catalog projections and scalar subqueries
	// reuse the existing catalog assertion shape without oidvector or join
	// expansion requirements.
	arguments := schema + ".cube," + schema + ".cube"
	procedure := func(name string) string {
		return cubeLiteral(schema+"."+name+"("+arguments+")") + "::regprocedure::oid"
	}
	operator := func(name string) string {
		return cubeLiteral(schema+"."+name+"("+arguments+")") + "::regoperator::oid"
	}
	namespace := cubeLiteral(schema) + "::regnamespace::oid"
	cube := cubeLiteral(schema+".cube") + "::regtype::oid"
	extensionID := "(SELECT oid FROM pg_extension WHERE extname='cube')"
	extensionNamespace := "(SELECT extnamespace FROM pg_extension WHERE extname='cube')"
	extensionOwner := "(SELECT extowner FROM pg_extension WHERE extname='cube')"
	var checks []fixture.Step
	for _, member := range []struct {
		procedure, operator, commutator string
		result                          int64
	}{
		{"cube_overlap", "&&", "&&", 16},
		{"cube_contains", "@>", "<@", 16},
		{"cube_contained", "<@", "@>", 16},
		{"cube_distance", "<->", "<->", 701},
		{"distance_taxicab", "<#>", "<#>", 701},
		{"distance_chebyshev", "<=>", "<=>", 701},
	} {
		procID, opID := procedure(member.procedure), operator(member.operator)
		procFacts := "SELECT proname AS name,pronargs::integer AS nargs,prorettype::integer AS result,proisstrict AS strict,provolatile::text AS volatility,proparallel::text AS parallel,prokind::text AS kind,oid>0 AS identity,(pronamespace=" + namespace + " AND pronamespace=" + extensionNamespace + " AND proowner=" + extensionOwner + ") AS authority FROM pg_proc WHERE oid=" + procID
		checks = append(checks, fixture.Select(procFacts,
			[]string{"name", "nargs", "result", "strict", "volatility", "parallel", "kind", "identity", "authority"},
			[]string{"NAME", "INT4", "INT4", "BOOL", "TEXT", "TEXT", "TEXT", "BOOL", "BOOL"},
			[][]any{{member.procedure, int64(2), member.result, true, "i", "s", "f", true, true}}))
		checks = append(checks, fixture.Select("SELECT deptype AS dependency FROM pg_depend WHERE classid=1255 AND objid="+procID+" AND refclassid=3079 AND refobjid="+extensionID,
			[]string{"dependency"}, []string{"CHAR"}, [][]any{{"e"}}))
		opFacts := "SELECT oprname AS name,oprresult::integer AS result,oprcode::oid=" + procID + " AS procedure,(oprleft=" + cube + " AND oprright=" + cube + ") AS signature,oprcom=" + operator(member.commutator) + " AS commutator,oid>0 AS identity,(oprnamespace=" + namespace + " AND oprnamespace=" + extensionNamespace + " AND oprowner=" + extensionOwner + ") AS authority,oprcanhash AS hash,oprcanmerge AS merge FROM pg_operator WHERE oid=" + opID
		checks = append(checks, fixture.Select(opFacts,
			[]string{"name", "result", "procedure", "signature", "commutator", "identity", "authority", "hash", "merge"},
			[]string{"NAME", "INT4", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"},
			[][]any{{member.operator, member.result, true, true, true, true, true, false, false}}))
		checks = append(checks, fixture.Select("SELECT deptype AS dependency FROM pg_depend WHERE classid=2617 AND objid="+opID+" AND refclassid=3079 AND refobjid="+extensionID,
			[]string{"dependency"}, []string{"CHAR"}, [][]any{{"e"}}))
	}
	return checks
}

func TestReferenceCubeSpatialRoutines(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "cube_spatial_provider")
	fixture.SchemaReferenceCleanup(t, "cube_spatial_shadow")
	cubeSpatialScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE SCHEMA cube_spatial_provider", 0))
		run(fixture.Exec("CREATE SCHEMA cube_spatial_shadow", 0))
		run(fixture.Exec("CREATE EXTENSION cube WITH SCHEMA cube_spatial_provider", 0))
		for _, step := range cubeSpatialCatalogChecks("cube_spatial_provider") {
			cubeSpatialBoth(t, p, b, c, step)
		}
		// Execute the six genuine support procedures rather than relying only on
		// catalog spellings. These are identities required by selected operators.
		for i, name := range []string{"cube_overlap", "cube_contains", "cube_contained", "cube_distance", "distance_taxicab", "distance_chebyshev"} {
			typ := "FLOAT8"
			want := []any{false, false, false, float64(5), float64(7), float64(4)}[i]
			if i < 3 {
				typ = "BOOL"
			}
			q := "SELECT cube_spatial_provider." + name + "('(0,0),(1,1)'::cube_spatial_provider.cube,'(4,5),(6,7)'::cube_spatial_provider.cube) AS result"
			cubeSpatialBoth(t, p, b, c, cubeSpatialSelect(b, q, []string{"result"}, []string{typ}, [][]any{{want}}))
		}
		run(fixture.Exec("SET search_path=pg_catalog", 0))
		q := "SELECT cube_spatial_provider.cube_distance('(3,4)'::cube_spatial_provider.cube,'()'::cube_spatial_provider.cube) AS distance,'(3,4)'::cube_spatial_provider.cube OPERATOR(cube_spatial_provider.<->) '()'::cube_spatial_provider.cube AS euclidean,'(0)'::cube_spatial_provider.cube OPERATOR(cube_spatial_provider.&&) '(0)'::cube_spatial_provider.cube AS overlap"
		cubeSpatialBoth(t, p, b, c, cubeSpatialSelect(b, q, []string{"distance", "euclidean", "overlap"}, []string{"FLOAT8", "FLOAT8", "BOOL"}, [][]any{{float64(5), float64(5), true}}))
		run(fixture.Exec("SET search_path=cube_spatial_shadow,cube_spatial_provider,pg_catalog", 0))
		q = "SELECT cube_distance('(3,4)'::cube,'()'::cube) AS distance,'(3,4)'::cube <-> '()'::cube AS euclidean"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		selected := cubeSpatialSelect(b, q, []string{"distance", "euclidean"}, []string{"FLOAT8", "FLOAT8"}, [][]any{{float64(5), float64(5)}})
		fixture.RunStep(t, p.Ctx, held, b.Recorder, selected)
		run(fixture.Exec("CREATE VIEW public.cube_spatial_resolved AS "+q, 0))
		run(fixture.Exec("CREATE FUNCTION cube_spatial_shadow.cube_distance(cube_spatial_provider.cube,cube_spatial_provider.cube) RETURNS float8 LANGUAGE SQL AS 'SELECT 99::float8'", 0))
		run(fixture.Exec("CREATE OPERATOR cube_spatial_shadow.<-> (LEFTARG=cube_spatial_provider.cube,RIGHTARG=cube_spatial_provider.cube,FUNCTION=cube_spatial_shadow.cube_distance)", 0))
		shadow := cubeSpatialSelect(b, q, []string{"distance", "euclidean"}, []string{"FLOAT8", "FLOAT8"}, [][]any{{float64(99), float64(99)}})
		cubeSpatialBoth(t, p, b, c, shadow)
		// Held callable lookup re-resolves the new routine/operator shadow on
		// both backends; the stored view below retains its captured identities.
		fixture.RunStep(t, p.Ctx, held, b.Recorder, shadow)
		cubeSpatialBoth(t, p, b, c, fixture.Select("SELECT distance,euclidean FROM public.cube_spatial_resolved", []string{"distance", "euclidean"}, []string{"FLOAT8", "FLOAT8"}, [][]any{{float64(5), float64(5)}}))
		// A stored view records dependencies on both the real procedure/operator.
		deps := fixture.Select("SELECT CASE refclassid WHEN 1255::oid THEN 'pg_proc' ELSE 'pg_operator' END AS class,count(*) AS n FROM pg_depend WHERE classid=2618 AND objid=(SELECT oid FROM pg_rewrite WHERE ev_class='public.cube_spatial_resolved'::regclass::oid AND rulename='_RETURN') AND refclassid IN (1255,2617) GROUP BY refclassid", []string{"class", "n"}, []string{"TEXT", "INT8"}, [][]any{{"pg_proc", int64(1)}, {"pg_operator", int64(1)}})
		deps.Bag = true
		cubeSpatialBoth(t, p, b, c, deps)
		run(fixture.Exec("DROP EXTENSION cube CASCADE", 0))
		cubeSpatialBoth(t, p, b, c, fixture.Select("SELECT relname FROM pg_class WHERE relname='cube_spatial_resolved'", []string{"relname"}, []string{"NAME"}, [][]any{}))
		run(fixture.Exec("CREATE EXTENSION cube WITH SCHEMA cube_spatial_provider", 0))
		for _, step := range cubeSpatialCatalogChecks("cube_spatial_provider") {
			cubeSpatialBoth(t, p, b, c, step)
		}
	})
}
