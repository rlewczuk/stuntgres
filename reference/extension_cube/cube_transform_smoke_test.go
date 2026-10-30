package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceCubeTransformFeatureSmoke(t *testing.T) {
	cubeConstructorScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
		cubeTransformStoredSetup(t,p,b,c)
		run := func(s fixture.Step) { fixture.RunStep(t,p.Ctx,c,b.Recorder,s) }
		run(fixture.Exec("CREATE TABLE cube_transform_smoke(id integer NOT NULL,s cube,u cube,i cube,e cube)",0))
		run(fixture.Exec("INSERT INTO cube_transform_smoke VALUES(1,"+cubeTransformCases[0].literal+","+cubeTransformCases[1].literal+","+cubeTransformCases[2].literal+","+cubeTransformCases[3].literal+")",1))
		q := "INSERT INTO cube_transform_smoke VALUES($1::integer,cube_subset($2::cube,$3::integer[]),cube_union($4::cube,$5::cube),cube_inter($4::cube,$5::cube),cube_enlarge($6::cube,$7::float8,$8::integer))"
		held := cubeConstructorHeld(t,p,c,q)
		defer held.Statement.Close()
		fixture.RunStep(t,p.Ctx,held,b.Recorder,fixture.Exec(q,1,2,"(1,3,5),(6,7,8)","{3,2,1,1}","(1,2)","(3,4)","(1,2),(3,4)",.5,3))
		fixture.RunStep(t,p.Ctx,held,b.Recorder,fixture.Exec(q,1,3,nil,nil,nil,nil,nil,nil,nil))
		baseline := cubeTransformTable(b,"cube_transform_smoke",[][]any{cubeTransformExpected(1),cubeTransformExpected(2),{int64(3),nil,nil,nil,nil}})
		run(baseline)
		run(cubeConstructorSelect(b,"SELECT cube_subset('(1,2),(1,3)'::cube,'{1}'::integer[]) AS v,cube_is_point(cube_subset('(1,2),(1,3)'::cube,'{1}'::integer[])) AS point",[]string{"v","point"},[]string{"CUBE","BOOL"},[][]any{{"(1)",true}}))
		run(cubeConstructorSelect(b,"SELECT cube_subset('(1),(2)'::cube,'{}'::integer[]) AS v,cube_is_point(cube_subset('(1),(2)'::cube,'{}'::integer[])) AS point",[]string{"v","point"},[]string{"CUBE","BOOL"},[][]any{{"()",true}}))
		run(cubeTransformSelect(b,"SELECT cube_enlarge('(2,-2),(-3,7)'::cube,-3,2) AS v","(-0.5, 1),(-0.5, 4)"))
		subsetSQL := "SELECT cube_subset($1::cube,$2::integer[]) AS v"
		subset := cubeConstructorHeld(t,p,c,subsetSQL)
		defer subset.Statement.Close()
		good := cubeTransformSelect(b,subsetSQL,"(1)","1","{1}")
		fixture.RunStep(t,p.Ctx,subset,b.Recorder,good)
		bad := fixture.ErrorDiagnostic(subsetSQL,cubeConstructorDiagnostic("2202E","Index out of bounds",""),"1","{0}")
		bad.SelectRows,bad.ErrorStage = true,"execute"
		fixture.RunStep(t,p.Ctx,subset,b.Recorder,bad)
		fixture.RunStep(t,p.Ctx,subset,b.Recorder,good)
		run(baseline)
		// Close held statements before native replacement or SQL recreation.
		subset.Statement.Close()
		held.Statement.Close()
		if b.PG { run(fixture.Exec("DROP TABLE cube_transform_smoke",0)) }
		cubeTransformControlCycle(t,p,b,c,false)
		if !b.PG { run(baseline) }
	})
}
