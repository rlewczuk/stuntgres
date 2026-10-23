package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceCubeConstructorFeatureSmoke(t *testing.T) {
	cubeConstructorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		cubeInstall(t, p, b, c)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(cubeConstructorSelect(b, "SELECT cube(ARRAY[1,2],ARRAY[3,4]) AS box,cube(1,1) AS point,cube_dim(cube(1,1)) AS dim,cube_is_point(cube(1,1)) AS is_point,cube(NULL::double precision) AS absent", []string{"box", "point", "dim", "is_point", "absent"}, []string{"CUBE", "CUBE", "INT4", "BOOL", "CUBE"}, [][]any{{"(1, 2),(3, 4)", "(1)", int64(1), true, nil}}))
		const table = "public.cube_construct_smoke"
		const view = "public.cube_construct_smoke_view"
		run(fixture.Exec("CREATE TABLE "+table+"(id integer NOT NULL,v cube DEFAULT cube(7),CONSTRAINT cube_construct_dim CHECK(cube_dim(v)<=3))", 0))
		run(fixture.Exec("CREATE VIEW "+view+" AS SELECT id,cube(v,5,6) AS extended,cube_dim(v) AS dim,cube_is_point(v) AS point FROM "+table, 0))
		run(fixture.Exec("INSERT INTO "+table+" VALUES(1,cube(ARRAY[1,2],ARRAY[3,4]))", 1))
		insertSQL := "INSERT INTO " + table + " VALUES($1,cube($2::double precision,$3::double precision))"
		insert := cubeConstructorHeld(t, p, c, insertSQL)
		defer insert.Statement.Close()
		fixture.RunStep(t, p.Ctx, insert, b.Recorder, fixture.Exec(insertSQL, 1, 2, 1, 1))
		fixture.RunStep(t, p.Ctx, insert, b.Recorder, fixture.Exec(insertSQL, 1, 3, nil, 1))
		insert.Statement.Close()
		rows := [][]any{{int64(1), "(1, 2),(3, 4)"}, {int64(2), "(1)"}, {int64(3), nil}}
		baseline := cubeSelect(b, "SELECT id,v FROM "+table+" ORDER BY id", []string{"id", "v"}, rows)
		run(baseline)
		q := "SELECT cube($1::double precision[],$2::double precision[]) AS v"
		held := cubeConstructorHeld(t, p, c, q)
		defer held.Statement.Close()
		good := cubeConstructorSelect(b, q, []string{"v"}, []string{"CUBE"}, [][]any{{"(1, 2),(3, 4)"}}, "{1,2}", "{3,4}")
		fixture.RunStep(t, p.Ctx, held, b.Recorder, good)
		for _, tc := range []struct {
			first, second string
			diagnostic    fixture.Diagnostic
		}{
			{"{1,2}", "{3}", cubeConstructorMismatch()},
			{cubeConstructorArray(101, "1"), cubeConstructorArray(100, "2"), cubeConstructorLimit("can't extend cube")},
		} {
			s := fixture.ErrorDiagnostic(q, tc.diagnostic, tc.first, tc.second)
			s.SelectRows, s.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, good)
		}
		// The legal boundary uses an independent 100-coordinate point witness.
		fixture.RunStep(t, p.Ctx, held, b.Recorder, cubeConstructorSelect(b, q, []string{"v"}, []string{"CUBE"}, [][]any{{cubeConstructorPoint(100, "1")}}, cubeConstructorArray(100, "1"), cubeConstructorArray(100, "1")))
		held.Statement.Close()
		for _, prepared := range []bool{false, true} {
			cubeError(t, p, b, c, "INSERT INTO "+table+" VALUES(4,cube(4)),(5,cube(ARRAY[1,2],ARRAY[3]))", prepared, false, false, cubeConstructorMismatch())
			run(baseline)
		}
		cubeConstructorControlCycle(t, p, b, c, table, view, "public.cube", rows, rows)
	})
}
