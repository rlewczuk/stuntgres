package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePolygonFeatureSmoke(t *testing.T) {
	polygonScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE polygon_smoke(id integer NOT NULL,v polygon)", 0))
		for i, v := range []string{polygonTriangle, "(0,0),(4,0),(0,4)", "(0,0,4,0,0,4)", "0,0,4,0,0,4"} {
			run(fixture.Exec("INSERT INTO polygon_smoke VALUES($1,$2)", 1, i+1, v))
		}
		q := "INSERT INTO polygon_smoke VALUES($1,$2)"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		buf := []byte(polygonRotated)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, 5, buf))
		copy(buf, "((9,9),(9,9),(9,9))")
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, 6, nil))
		check := polygonSelect(b, "SELECT id,v FROM polygon_smoke ORDER BY id", []string{"id", "v"}, []string{"INT4", "POLYGON"}, [][]any{{int64(1), polygonTriangle}, {int64(2), polygonTriangle}, {int64(3), polygonTriangle}, {int64(4), polygonTriangle}, {int64(5), polygonRotated}, {int64(6), nil}})
		run(check)
		run(fixture.Prepared(check))
		behavior := fixture.Select("SELECT '((0,0),(4,0),(0,4))'::polygon ~= '((4,0),(0,4),(0,0))'::polygon AS rotated,'((0,0),(4,0),(0,4))'::polygon ~= '((1,1),(5,1),(1,5))'::polygon AS translated,'((0,0),(4,0),(0,4))'::polygon @> '(1,1)'::point AS interior,'(2,2)'::point <@ '((0,0),(4,0),(0,4))'::polygon AS boundary,'((0,0),(4,0),(0,4))'::polygon @> '(3,3)'::point AS exterior,NULL::polygon ~= '((0,0),(4,0),(0,4))'::polygon AS unknown", []string{"rotated", "translated", "interior", "boundary", "exterior", "unknown"}, []string{"BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, false, true, true, false, nil}})
		run(behavior)
		run(fixture.Prepared(behavior))
		d := polygonInputDiagnostic("bad")
		d.Context = "unnamed portal parameter $2 = '...'"
		s := fixture.ErrorDiagnostic(q, d, 7, "bad")
		s.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		run(fixture.Select("SELECT count(*) AS n FROM polygon_smoke", []string{"n"}, []string{"INT8"}, [][]any{{int64(6)}}))
		polygonPersistentWitness(t, p, b, c)
		// The control cycle also restores the smoke table; prove its independent
		// ordered rows and runtime predicates remain executable afterwards.
		run(check)
		run(behavior)
	})
}
