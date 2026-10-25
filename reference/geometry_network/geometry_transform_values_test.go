package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceGeometryTransformValues(t *testing.T) {
	// Fixed coordinate arithmetic, independently specified before oracle discovery.
	for _, tc := range []struct {
		typ, a string
		want   [4]string
	}{
		{"point", "(1,2)", [4]string{"(1,4)", "(1,0)", "(-4,2)", "(1,-0.5)"}},
		{"box", "(2,1),(0,0)", [4]string{"(2,3),(0,2)", "(2,-1),(0,-2)", "(0,4),(-2,0)", "(0.5,0),(0,-1)"}},
		{"path", "[(1,2),(3,4)]", [4]string{"[(1,4),(3,6)]", "[(1,0),(3,2)]", "[(-4,2),(-8,6)]", "[(1,-0.5),(2,-1.5)]"}},
		{"circle", "<(1,2),2>", [4]string{"<(1,4),2>", "<(1,0),2>", "<(-4,2),4>", "<(1,-0.5),1>"}},
	} {
		for i, op := range []string{"+", "-", "*", "/"} {
			t.Run(tc.typ+op, func(t *testing.T) {
				geometryTransformScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
					typ := strings.ToUpper(tc.typ)
					literal := "SELECT " + tc.typ + " " + boxQuote(tc.a) + " " + op + " point '(0,2)' AS v"
					s := geometryTransformSelect(b, literal, []string{"v"}, []string{typ}, [][]any{{tc.want[i]}})
					run(s)
					run(fixture.Prepared(s))
					run(geometryTransformSelect(b, literal+" WHERE FALSE", []string{"v"}, []string{typ}, nil))
					run(fixture.Exec("CREATE TABLE geom_transform_empty(a "+tc.typ+",p point)", 0))
					run(geometryTransformSelect(b, "SELECT a "+op+" p AS v FROM geom_transform_empty", []string{"v"}, []string{typ}, nil))
					q := "SELECT $1::" + tc.typ + " " + op + " $2::point AS v"
					stmt, err := c.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
					for _, args := range [][]any{{tc.a, "(0,2)"}, {nil, "(0,2)"}, {tc.a, nil}, {nil, nil}, {[]byte(tc.a), []byte("(0,2)")}, {tc.a, "(0,0)"}, {tc.a, "(0,2)"}} {
						var want any = tc.want[i]
						if args[0] == nil || args[1] == nil {
							want = nil
						}
						if args[1] == "(0,0)" {
							if op == "/" {
								s := boxError(q, "22012", "division by zero", "", "", false, true, args...)
								run(s)
								run(fixture.Prepared(s))
								fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
								continue
							}
							if op == "*" {
								switch tc.typ {
								case "point":
									want = "(0,0)"
								case "box":
									want = "(0,0),(0,0)"
								case "path":
									want = "[(0,0),(0,0)]"
								case "circle":
									want = "<(0,0),0>"
								}
							} else {
								want = tc.a
							}
						}
						s := geometryTransformSelect(b, q, []string{"v"}, []string{typ}, [][]any{{want}}, args...)
						run(s)
						run(fixture.Prepared(s))
						fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
					}
					for mask := 1; mask < 4; mask++ {
						a, z := tc.typ+" "+boxQuote(tc.a), "point '(0,2)'"
						if mask&1 != 0 {
							a = "NULL::" + tc.typ
						}
						if mask&2 != 0 {
							z = "NULL::point"
						}
						s := geometryTransformSelect(b, "SELECT "+a+" "+op+" "+z+" AS v", []string{"v"}, []string{typ}, [][]any{{nil}})
						run(s)
						run(fixture.Prepared(s))
					}
				})
			})
		}
	}
	t.Run("structure_and_selection", func(t *testing.T) {
		geometryTransformScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			for _, tc := range []struct{ expr, typ, want string }{
				{"point '(1,2)' OPERATOR(pg_catalog.*) point '(3,4)'", "POINT", "(-5,10)"},
				{"point '(1,2)' / '(1,1)'", "POINT", "(1.5,0.5)"},
				{"path '((1,2),(1,2),(3,4))' + point '(0,2)'", "PATH", "((1,4),(1,4),(3,6))"},
				{"path '((1,2),(1,2),(3,4))' - point '(0,2)'", "PATH", "((1,0),(1,0),(3,2))"},
				{"path '((1,2),(1,2),(3,4))' * point '(0,2)'", "PATH", "((-4,2),(-4,2),(-8,6))"},
				{"path '((1,2),(1,2),(3,4))' / point '(0,2)'", "PATH", "((1,-0.5),(1,-0.5),(2,-1.5))"},
			} {
				s := geometryTransformSelect(b, "SELECT "+tc.expr+" AS v", []string{"v"}, []string{tc.typ}, [][]any{{tc.want}})
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
			}
		})
	})
}
