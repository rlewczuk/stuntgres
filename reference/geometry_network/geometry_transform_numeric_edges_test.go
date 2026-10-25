package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceGeometryTransformNumericEdges(t *testing.T) {
	// REL_18_6 float.h checks each intermediate, not merely the final coordinate.
	cases := []struct{ typ, op, a, z, state, result string }{
		{"point", "/", "(1,2)", "(0,0)", "22012", ""},
		{"point", "/", "(1,2)", "(-0,-0)", "22012", ""},
		{"box", "/", "(2,1),(0,0)", "(0,0)", "22012", ""},
		{"path", "/", "[(1,2),(3,4)]", "(0,0)", "22012", ""},
		{"circle", "/", "<(1,2),2>", "(0,0)", "22012", ""},
		{"point", "/", "(NaN,1)", "(0,0)", "", "(NaN,NaN)"},
		{"circle", "/", "<(NaN,1),2>", "(0,0)", "22012", ""},
		{"point", "/", "(1,2)", "(Infinity,0)", "", "(NaN,NaN)"},
		{"point", "/", "(1,2)", "(NaN,0)", "", "(NaN,NaN)"},
		{"point", "*", "(Infinity,1)", "(0,0)", "", "(NaN,NaN)"},
		{"point", "/", "(1,2)", "(1e200,0)", "overflow", ""},
		{"point", "/", "(1,2)", "(1e-200,0)", "underflow", ""},
		{"point", "+", "(1e308,0)", "(1e308,0)", "overflow", ""},
		{"point", "*", "(1e308,1e308)", "(2,2)", "overflow", ""},
		{"point", "*", "(NaN,1e308)", "(0,2)", "overflow", ""},
		{"point", "*", "(1e-200,0)", "(1e-200,0)", "underflow", ""},
		{"circle", "*", "<(0,0),1>", "(1.7e308,1.7e308)", "overflow", ""},
		{"circle", "*", "<(0,0),1e308>", "(2,0)", "overflow", ""},
		{"circle", "*", "<(0,0),1e-200>", "(1e-200,0)", "underflow", ""},
		{"circle", "/", "<(0,0),1e-200>", "(1e150,0)", "underflow", ""},
		{"circle", "*", "<(0,0),1>", "(Infinity,NaN)", "", "<(NaN,NaN),Infinity>"},
		{"circle", "*", "<(0,0),0>", "(Infinity,NaN)", "", "<(NaN,NaN),NaN>"},
		{"circle", "*", "<(0,0),1>", "(1e150,1e-200)", "", "<(0,0),1e+150>"},
	}
	for i, tc := range cases {
		t.Run(fixture.Position(i+1), func(t *testing.T) {
			geometryTransformScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				q := "SELECT $1::" + tc.typ + " " + tc.op + " $2::point AS v"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				step := func(query string, args ...any) fixture.Step {
					if tc.state == "" {
						return geometryTransformSelect(b, query, []string{"v"}, []string{strings.ToUpper(tc.typ)}, [][]any{{tc.result}}, args...)
					}
					state, message := tc.state, "division by zero"
					if state != "22012" {
						state = "22003"
						message = "value out of range: " + tc.state
					}
					return boxError(query, state, message, "", "", false, true, args...)
				}
				s := step(q, tc.a, tc.z)
				run(s)
				run(fixture.Prepared(s))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				literal := "SELECT " + tc.typ + " " + boxQuote(tc.a) + " " + tc.op + " point " + boxQuote(tc.z) + " AS v"
				s = step(literal)
				run(s)
				run(fixture.Prepared(s))
				// Strictness suppresses provider arithmetic, not expression-child errors.
				for _, args := range [][]any{{nil, tc.z}, {tc.a, nil}, {nil, nil}} {
					s := geometryTransformSelect(b, q, []string{"v"}, []string{strings.ToUpper(tc.typ)}, [][]any{{nil}}, args...)
					run(s)
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				}
			})
		})
	}
	// Compare a surviving subnormal numerically rather than guessing geometric text.
	t.Run("subnormal", func(t *testing.T) {
		geometryTransformScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			q := "SELECT (point '(1e-150,0)' * point '(1e-160,0)')[0] AS v"
			s := fixture.Select(q, []string{"v"}, []string{"FLOAT8"}, [][]any{{float64(1e-310)}})
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
		})
	})
}
