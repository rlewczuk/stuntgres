package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceCircleInputOutput(t *testing.T) {
	circleScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE circle_io(id integer NOT NULL,v circle)", 0))
		forms := []string{"<(1,2),3>", "((1,2),3)", "(1,2),3", "1,2,3", " < ( 1 , 2 ) , 3 > ", " ( ( 1 , 2 ) , 3 ) ", " (1, 2) , 3 ", " 1 , 2 , 3 "}
		var want [][]any
		id := 0
		for _, input := range forms {
			for route := range 3 {
				id++
				q := "INSERT INTO circle_io VALUES ($1,$2)"
				s := fixture.Exec(q, 1, id, input)
				if route == 0 {
					s = fixture.Exec(fmt.Sprintf("INSERT INTO circle_io VALUES (%d,%s)", id, circleLiteral(input)), 1)
				}
				if route == 2 {
					s = fixture.Prepared(s)
				}
				run(s)
				want = append(want, []any{int64(id), "<(1,2),3>", float64(3)})
			}
		}
		run(circleSelect(b, "SELECT id,v,radius(v) AS r FROM circle_io ORDER BY id", []string{"id", "v", "r"}, []string{"INT4", "CIRCLE", "FLOAT8"}, want))
		vectors := []struct{ input, output string }{
			{"-0,0,-0", "<(-0,0),-0>"}, {"0,0,0", "<(0,0),0>"},
			{"NaN,Infinity,NaN", "<(NaN,Infinity),NaN>"}, {"-Infinity,+Infinity,+Inf", "<(-Infinity,Infinity),Infinity>"},
			{"+1e1,-2e-1,3e0", "<(10,-0.2),3>"},
			{"0.10000000000000002,5e-324,1.7976931348623157e308", "<(0.10000000000000002,5e-324),1.7976931348623157e+308>"},
		}
		for i, v := range vectors {
			t.Run(fmt.Sprintf("numeric_%d", i), func(t *testing.T) {
				for _, prepared := range []bool{false, true} {
					s := circleSelect(b, "SELECT $1::circle AS v,CAST($1 AS circle) AS w", []string{"v", "w"}, []string{"CIRCLE", "CIRCLE"}, [][]any{{v.output, v.output}}, v.input)
					if prepared {
						s = fixture.Prepared(s)
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				}
			})
		}
		// Source-derived permissive forms: root's pinned run establishes their contract.
		for _, input := range []string{"<(1,2)3>", "((1,2)3)", "<(1,2),3)", "((1,2),3>"} {
			t.Run("permissive_"+input, func(t *testing.T) {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, circleSelect(b, "SELECT $1::circle AS v", []string{"v"}, []string{"CIRCLE"}, [][]any{{"<(1,2),3>"}}, input))
			})
		}
		for _, v := range []struct{ input, output string }{{"0x1p0,0,1", "<(1,0),1>"}, {"+NaN,0,1", "<(NaN,0),1>"}, {"NaN(payload),0,1", "<(NaN,0),1>"}} {
			t.Run("strtod_"+v.input, func(t *testing.T) {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, circleSelect(b, "SELECT $1::circle AS v", []string{"v"}, []string{"CIRCLE"}, [][]any{{v.output}}, v.input))
			})
		}
		run(fixture.Exec("INSERT INTO circle_io VALUES (100,'0.10000000000000002,0,1')", 1))
		run(fixture.Exec("SET extra_float_digits=0", 0))
		run(circleSelect(b, "SELECT v FROM circle_io WHERE id=100", []string{"v"}, []string{"CIRCLE"}, [][]any{{"<(0.1,0),1>"}}))
		run(fixture.Exec("SET extra_float_digits=3", 0))
		run(circleSelect(b, "SELECT v FROM circle_io WHERE id=100", []string{"v"}, []string{"CIRCLE"}, [][]any{{"<(0.10000000000000002,0),1>"}}))
	})
}
