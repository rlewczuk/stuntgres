package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func cubeTransformCatalogChecks(schema string, owned bool) []fixture.Step {
	var checks []fixture.Step
	for _, tc := range []struct { name,args string; nargs int64 }{
		{"cube_subset",schema+".cube,integer[]",2},
		{"cube_union",schema+".cube,"+schema+".cube",2},
		{"cube_inter",schema+".cube,"+schema+".cube",2},
		{"cube_enlarge",schema+".cube,double precision,integer",3},
	} {
		signature := schema+"."+tc.name+"("+tc.args+")"
		q := "SELECT p.proname,p.pronargs,p.prorettype="+cubeLiteral(schema+".cube")+"::regtype::oid AS result_identity,p.proisstrict,p.provolatile,p.proparallel,p.proretset,p.prokind,p.oid>0 AS identity,n.nspname,p.proowner=e.extowner AS same_owner,d.deptype FROM (SELECT * FROM pg_proc WHERE oid="+cubeLiteral(signature)+"::regprocedure::oid) p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_depend d ON d.classid=1255 AND d.objid=p.oid AND d.refclassid=3079 JOIN pg_extension e ON e.oid=d.refobjid WHERE e.extname='cube'"
		checks = append(checks,fixture.Select(q,[]string{"proname","pronargs","result_identity","proisstrict","provolatile","proparallel","proretset","prokind","identity","nspname","same_owner","deptype"},[]string{"NAME","INT2","BOOL","BOOL","CHAR","CHAR","BOOL","CHAR","BOOL","NAME","BOOL","CHAR"},[][]any{{tc.name,tc.nargs,true,true,"i","s",false,"f",true,schema,!owned,"e"}}))
	}
	return checks
}

func TestReferenceCubeTransformBinding(t *testing.T) {
	t.Run("catalog_and_coercion",func(t *testing.T) {
		cubeConstructorScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
			cubeInstall(t,p,b,c)
			run := func(s fixture.Step) { fixture.RunStep(t,p.Ctx,c,b.Recorder,s) }
			for _, s := range cubeTransformCatalogChecks("public",false) { run(s); run(fixture.Prepared(s)) }
			for _, tc := range []struct { expression string; want any; args []any }{
				{"cube_subset('(1,2)','{2,1}')","(2, 1)",nil},
				{"cube_union('(1)','(2)')","(1),(2)",nil},
				{"cube_inter('(1)','(2)')","(2),(1)",nil},
				{"cube_enlarge('(1)',1,2)","(0, -1),(2, 1)",nil},
				{"cube_subset($1,$2)","(2, 1)",[]any{"(1,2)","{2,1}"}},
				{"cube_union($1,$2)","(1),(2)",[]any{"1","2"}},
				{"cube_inter($1,$2)","(2),(1)",[]any{"1","2"}},
				{"cube_enlarge($1,$2,$3)","(0, -1),(2, 1)",[]any{"1",1,2}},
			} {
				s := cubeTransformSelect(b,"SELECT "+tc.expression+" AS v",tc.want,tc.args...)
				run(s); run(fixture.Prepared(s))
			}
			run(fixture.Exec("CREATE TABLE cube_transform_binding(id integer,v cube)",0))
			run(fixture.Exec("INSERT INTO cube_transform_binding VALUES(1,'1'),(2,NULL)",2))
			for _, tc := range cubeTransformCases {
				for _, pred := range []string{"id=1","id=2","id=99"} {
					// Exact computed metadata is checked even with no source rows.
					var rows [][]any
					if pred != "id=99" { rows = [][]any{{nil}} }
					s := cubeConstructorSelect(b,"SELECT "+tc.nameExpression("v")+" AS v FROM cube_transform_binding WHERE "+pred,[]string{"v"},[]string{"CUBE"},rows)
					if pred == "id=1" { s.Rows = [][]any{{map[string]string{"subset":"(1)","union":"(1),(2)","inter":"(2),(1)","enlarge":"(0),(2)"}[tc.name]}} }
					s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { cubeMetadata(t,b.PG,rows,[]string{"v"},nil) }
					run(s); run(fixture.Prepared(s))
				}
			}
		})
	})
	t.Run("namespace_and_shadow",func(t *testing.T) {
		fixture.SchemaReferenceCleanup(t,"cube_transform_ns")
		fixture.SchemaReferenceCleanup(t,"cube_transform_shadow")
		cubeConstructorScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t,p.Ctx,c,b.Recorder,s) }
			run(fixture.Exec("CREATE SCHEMA cube_transform_ns",0))
			run(fixture.Exec("CREATE SCHEMA cube_transform_shadow",0))
			run(fixture.Exec("CREATE EXTENSION cube SCHEMA cube_transform_ns",0))
			run(fixture.Exec("SET search_path=cube_transform_ns,public",0))
			for _, s := range cubeTransformCatalogChecks("cube_transform_ns",false) { run(s) }
			for _, tc := range cubeTransformCases {
				s := cubeTransformSelect(b,"SELECT "+tc.literal+" AS v",tc.want)
				run(s); run(fixture.Prepared(s))
			}
			run(fixture.Exec("CREATE FUNCTION cube_transform_shadow.cube_union(cube_transform_ns.cube,cube_transform_ns.cube) RETURNS cube_transform_ns.cube BEGIN ATOMIC SELECT '(99)'::cube_transform_ns.cube; END",0))
			run(fixture.Exec("SET search_path=cube_transform_shadow,cube_transform_ns,public",0))
			run(cubeTransformSelect(b,"SELECT cube_union('1'::cube,'2'::cube) AS v","(99)"))
			run(cubeTransformSelect(b,"SELECT cube_transform_ns.cube_union('1'::cube,'2'::cube) AS v","(1),(2)"))
			run(fixture.Exec("SET search_path=pg_catalog",0))
			run(cubeTransformSelect(b,"SELECT cube_transform_ns.cube_subset('(1,2)'::cube_transform_ns.cube,'{2,1}'::integer[]) AS v","(2, 1)"))
		})
	})
	for _, tc := range []struct { name,q,code,message,hint string; installed bool }{
		{"before_install","SELECT cube_union(1,2)","42883","function cube_union(integer, integer) does not exist","No function matches the given name and argument types. You might need to add explicit type casts.",false},
		{"arity","SELECT cube_union('1'::cube)","42883","function cube_union(cube) does not exist","No function matches the given name and argument types. You might need to add explicit type casts.",true},
		{"typed","SELECT cube_subset(NULL::cube,'{1}'::text)","42883","function cube_subset(cube, text) does not exist","No function matches the given name and argument types. You might need to add explicit type casts.",true},
		{"named","SELECT cube_union(a => '1'::cube,b => '2'::cube)","42883","function cube_union(a => cube, b => cube) does not exist","No function matches the given name and argument types. You might need to add explicit type casts.",true},
		{"missing_schema","SELECT cube_transform_missing.cube_union('1'::cube,'2'::cube)","3F000",`schema "cube_transform_missing" does not exist`,"",true},
		{"distinct","SELECT cube_union(DISTINCT '1'::cube,'2'::cube)","42809","DISTINCT specified, but cube_union is not an aggregate function","",true},
		{"filter","SELECT cube_union('1'::cube,'2'::cube) FILTER (WHERE true)","42809","FILTER specified, but cube_union is not an aggregate function","",true},
		{"over","SELECT cube_union('1'::cube,'2'::cube) OVER ()","42809","OVER specified, but cube_union is not a window function nor an aggregate function","",true},
	} {
		t.Run(tc.name,func(t *testing.T) {
			cubeConstructorScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
				if tc.installed { cubeInstall(t,p,b,c) }
				want := fixture.ControlsReferenceDiagnostic(tc.code,tc.message)
				want.Hint,want.Position = tc.hint,"8"
				for _, prepared := range []bool{false,true} { cubeError(t,p,b,c,tc.q,prepared,true,true,want) }
			})
		})
	}
}

func (tc cubeTransformCase) nameExpression(value string) string {
	switch tc.name {
	case "subset": return "cube_subset("+value+",'{1}'::integer[])"
	case "union": return "cube_union("+value+",'2'::cube)"
	case "inter": return "cube_inter("+value+",'2'::cube)"
	default: return "cube_enlarge("+value+",1,1)"
	}
}
