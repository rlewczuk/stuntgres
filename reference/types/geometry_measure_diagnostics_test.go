package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferenceGeometryMeasureDiagnostics(t *testing.T) {
	geometryMeasureScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		const hint = "No function matches the given name and argument types. You might need to add explicit type casts."
		for _, tc := range []struct{ expression, signature string }{
			{"area(polygon '((0,0),(1,0),(0,1))')", "area(polygon)"},
			{"center(lseg '[(0,0),(1,1)]')", "center(lseg)"},
			{"center(path '((0,0))')", "center(path)"},
			{"diameter(box '(1,1),(0,0)')", "diameter(box)"},
			{"area()", "area()"},
			{"area(box '(1,1),(0,0)',box '(1,1),(0,0)')", "area(box, box)"},
			{"area('x'::text)", "area(text)"},
			{"center('x'::text)", "center(text)"},
			{"diameter('x'::text)", "diameter(text)"},
			{"radius('x'::text)", "radius(text)"},
			{"area(missing => box '(1,1),(0,0)')", "area(missing => box)"},
		} {
			for _, prepared := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/prepared=%t", tc.expression, prepared), func(t *testing.T) {
					fixture.RunStep(t, p.Ctx, c, b.Recorder, boxError("SELECT "+tc.expression, "42883", "function "+tc.signature+" does not exist", "8", hint, prepared, false))
				})
			}
		}
		for _, name := range []string{"area", "center"} {
			for _, prepared := range []bool{false, true} {
				for _, operand := range []string{"NULL", "$1"} {
					t.Run(fmt.Sprintf("ambiguous/%s/%s/%t", name, operand, prepared), func(t *testing.T) {
						var args []any
						if operand == "$1" {
							args = []any{nil}
						}
						s := boxError("SELECT "+name+"("+operand+")", "42725", "function "+name+"(unknown) is not unique", "8", "Could not choose a best candidate function. You might need to add explicit type casts.", prepared, false, args...)
						fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					})
				}
			}
		}
		for _, tc := range []struct{ expression, message string }{
			{"area(DISTINCT box '(1,1),(0,0)')", "DISTINCT specified, but area is not an aggregate function"},
			{"area(box '(1,1),(0,0)') FILTER (WHERE true)", "FILTER specified, but area is not an aggregate function"},
			{"area(box '(1,1),(0,0)') OVER ()", "OVER specified, but area is not a window function nor an aggregate function"},
		} {
			for _, prepared := range []bool{false, true} {
				t.Run(fmt.Sprintf("decoration/%s/%t", tc.expression, prepared), func(t *testing.T) {
					fixture.RunStep(t, p.Ctx, c, b.Recorder, boxError("SELECT "+tc.expression, "42809", tc.message, "8", "", prepared, false))
				})
			}
		}
		for _, tc := range []struct{ function, formal, bad, good, outcome string }{
			{"area", "box", "(1e308,1e308),(0,0)", "(2,2),(0,0)", "overflow"},
			{"area", "box", "(1e308,1),(-1e308,0)", "(2,2),(0,0)", "overflow"},
			{"center", "box", "(1e308,0),(1e308,0)", "(2,2),(0,0)", "overflow"},
			{"center", "box", "(5e-324,0),(0,0)", "(2,2),(0,0)", "underflow"},
			{"diameter", "circle", "<(0,0),1e308>", "<(0,0),2>", "overflow"},
			{"area", "circle", "<(0,0),1e308>", "<(0,0),2>", "overflow"},
			{"area", "circle", "<(0,0),1e-200>", "<(0,0),2>", "underflow"},
			{"area", "path", "((1e308,1e308))", "((0,0),(4,0),(0,3))", "overflow"},
			{"area", "path", "((1e-200,0),(0,1e-200),(0,0))", "((0,0),(4,0),(0,3))", "underflow"},
			{"area", "path", "((5e-324,0),(0,1),(0,0))", "((0,0),(4,0),(0,3))", "underflow"},
		} {
			t.Run(tc.function+"/"+tc.bad, func(t *testing.T) {
				q := "SELECT " + tc.function + "(" + tc.formal + " '" + tc.bad + "')"
				for _, prepared := range []bool{false, true} {
					fixture.RunStep(t, p.Ctx, c, b.Recorder, boxError(q, "22003", "value out of range: "+tc.outcome, "", "", prepared, true))
				}
				q = "SELECT " + tc.function + "($1::" + tc.formal + ") AS v"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				s := boxError(q, "22003", "value out of range: "+tc.outcome, "", "", false, true, tc.bad)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				typ, want := "FLOAT8", any(float64(4))
				if tc.function == "center" {
					typ, want = "POINT", "(1,1)"
				}
				if tc.formal == "path" {
					want = float64(6)
				}
				if tc.formal == "circle" && tc.function == "area" {
					want = float64(12.566370614359172)
				}
				fixture.RunStep(t, p.Ctx, held, b.Recorder, geometryMeasureSelect(b, q, []string{"v"}, []string{typ}, [][]any{{nil}}, nil))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, geometryMeasureSelect(b, q, []string{"v"}, []string{typ}, [][]any{{want}}, tc.good))
			})
		}
		for _, tc := range []struct{ expression, formal, input string }{
			{"area(box 'bad')", "box", "bad"},
			{"area(path '[]')", "path", "[]"},
			{"center(circle '<(0,0),-1>')", "circle", "<(0,0),-1>"},
			{"center(pg_catalog.circle '<(0,0),-1>')", "circle", "<(0,0),-1>"},
		} {
			for _, prefix := range []string{"SELECT ", "SELECT /* é */ "} {
				q := prefix + tc.expression
				position := fixture.Position(utf8.RuneCountInString(q[:strings.Index(q, "'")]) + 1)
				for _, prepared := range []bool{false, true} {
					t.Run(fmt.Sprintf("input/%s/%s/%t", tc.formal, prefix, prepared), func(t *testing.T) {
						fixture.RunStep(t, p.Ctx, c, b.Recorder, boxError(q, "22P02", "invalid input syntax for type "+tc.formal+": \""+tc.input+"\"", position, "", prepared, false))
					})
				}
			}
		}
		for _, token := range []string{"1e400", "1e-400"} {
			for _, formal := range []string{"circle", "pg_catalog.circle"} {
				for _, prefix := range []string{"SELECT ", "SELECT /* é */ "} {
					q := prefix + "area(" + formal + " '<(0,0)," + token + ">')"
					position := fixture.Position(utf8.RuneCountInString(q[:strings.Index(q, "'")]) + 1)
					for _, prepared := range []bool{false, true} {
						t.Run(fmt.Sprintf("numeric/%s/%s/%s/%t", token, formal, prefix, prepared), func(t *testing.T) {
							fixture.RunStep(t, p.Ctx, c, b.Recorder, boxError(q, "22003", "\""+token+"\" is out of range for type double precision", position, "", prepared, false))
						})
					}
				}
			}
		}
	})
}
