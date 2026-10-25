package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePointFeatureSmoke(t *testing.T) {
	pointScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE point_smoke(id integer,p point)", 0))
		run(fixture.Exec("INSERT INTO point_smoke VALUES(1,'(1,2)'),(2,'1,2')", 2))
		check := func(first string) {
			run(boxSelect(b, "SELECT id,p FROM point_smoke ORDER BY id", []string{"id", "p"}, []string{"INT4", "POINT"}, [][]any{{int64(1), first}, {int64(2), "(1,2)"}}))
		}
		check("(1,2)")
		q := "SELECT $1::point AS p"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, tc := range []struct{ arg, want any }{{"1,2", "(1,2)"}, {[]byte("1,2"), "(1,2)"}, {nil, nil}} {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, boxSelect(b, q, []string{"p"}, []string{"POINT"}, [][]any{{tc.want}}, tc.arg))
		}
		d := fixture.ControlsReferenceDiagnostic("22P02", "invalid input syntax for type point: \"bad\"")
		d.Context = "unnamed portal parameter $1 = '...'"
		s := fixture.ErrorDiagnostic(q, d, "bad")
		s.SelectRows, s.ErrorStage = true, "execute"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, boxSelect(b, q, []string{"p"}, []string{"POINT"}, [][]any{{"(1,2)"}}, "1,2"))
		run(boxError("SELECT point 'bad'", "22P02", d.Message, "14", "", false, false))
		want := []any{true, false, false, false, false, false, true, true, false, false, false}
		for i, pred := range pointPredicates {
			run(fixture.Select("SELECT point '1,2' "+pred.op+" point '1,2' AS v", []string{"v"}, []string{"BOOL"}, [][]any{{want[i]}}))
		}
		run(fixture.Select("SELECT p[0] AS x,p[1] AS y FROM point_smoke WHERE id=1", []string{"x", "y"}, []string{"FLOAT8", "FLOAT8"}, [][]any{{float64(1), float64(2)}}))
		var old *sql.Rows
		if !b.PG {
			old, err = c.QueryContext(p.Ctx, "SELECT id,p FROM point_smoke ORDER BY id")
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
		}
		run(fixture.Exec("UPDATE point_smoke SET p[0]=-5 WHERE id=1", 1))
		check("(-5,2)")
		if b.PG {
			return
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		check("(-5,2)")
		fixture.PointConsumeRetained(t, old, [][]any{{int64(1), "(1,2)"}, {int64(2), "(1,2)"}})
	})
}
