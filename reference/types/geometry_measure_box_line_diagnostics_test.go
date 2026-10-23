package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferenceGeometryMeasureBoxLineDiagnostics(t *testing.T) {
	geometryMeasureScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		const hint = "No function matches the given name and argument types. You might need to add explicit type casts."
		for _, tc := range []struct{ expression, signature string }{
			{"slope(line '{1,-1,0}')", "slope(line)"},
			{"slope(lseg '[(0,0),(2,1)]')", "slope(lseg)"},
			{"width(point '(1,2)')", "width(point)"},
			{"height(lseg '[(0,0),(2,1)]')", "height(lseg)"},
			{"diagonal(line '{1,-1,0}')", "diagonal(line)"},
			{"slope(point '(0,0)',1)", "slope(point, integer)"},
			{"slope(1,point '(0,0)')", "slope(integer, point)"},
			{"width()", "width()"},
			{"height(box '(4,6),(1,2)',box '(4,6),(1,2)')", "height(box, box)"},
			{"diagonal('x'::text)", "diagonal(text)"},
			{"slope(point '(0,0)')", "slope(point)"},
			{"width(missing => box '(4,6),(1,2)')", "width(missing => box)"},
		} {
			for _, prepared := range []bool{false, true} {
				t.Run(fmt.Sprintf("signature/%s/%t", tc.signature, prepared), func(t *testing.T) {
					fixture.RunStep(t, p.Ctx, c, b.Recorder, boxError("SELECT "+tc.expression, "42883", "function "+tc.signature+" does not exist", "8", hint, prepared, false))
				})
			}
		}
		for _, tc := range []struct{ expression, message string }{
			{"width(DISTINCT box '(4,6),(1,2)')", "DISTINCT specified, but width is not an aggregate function"},
			{"slope(point '(0,0)',point '(2,1)') FILTER (WHERE true)", "FILTER specified, but slope is not an aggregate function"},
			{"diagonal(box '(4,6),(1,2)') OVER ()", "OVER specified, but diagonal is not a window function nor an aggregate function"},
		} {
			for _, prepared := range []bool{false, true} {
				t.Run(fmt.Sprintf("decoration/%s/%t", tc.expression, prepared), func(t *testing.T) {
					fixture.RunStep(t, p.Ctx, c, b.Recorder, boxError("SELECT "+tc.expression, "42809", tc.message, "8", "", prepared, false))
				})
			}
		}
		for _, expression := range []string{"width(box 'bad')", "height(box 'bad')", "diagonal(box 'bad')", "slope(point 'bad',point '(2,1)')", "slope(point '(0,0)',point 'bad')"} {
			for _, prefix := range []string{"SELECT ", "SELECT /* é */ "} {
				q := prefix + expression
				pos := fixture.Position(utf8.RuneCountInString(q[:strings.Index(q, "'bad'")]) + 1)
				formal := "box"
				if strings.HasPrefix(expression, "slope") {
					formal = "point"
				}
				for _, prepared := range []bool{false, true} {
					t.Run(fmt.Sprintf("literal/%s/%s/%t", expression, prefix, prepared), func(t *testing.T) {
						fixture.RunStep(t, p.Ctx, c, b.Recorder, boxError(q, "22P02", "invalid input syntax for type "+formal+": \"bad\"", pos, "", prepared, false))
					})
				}
			}
		}
		for _, tc := range []struct {
			expression, call string
			args, good       []any
			want             float64
		}{
			{"width(box '(1e308,0),(-1e308,0)')", "width($1::box)", []any{"(1e308,0),(-1e308,0)"}, []any{"(4,6),(1,2)"}, 3},
			{"height(box '(0,1e308),(0,-1e308)')", "height($1::box)", []any{"(0,1e308),(0,-1e308)"}, []any{"(4,6),(1,2)"}, 4},
			{"slope(point '(0,1e308)',point '(1,-1e308)')", "slope($1::point,$2::point)", []any{"(0,1e308)", "(1,-1e308)"}, []any{"(0,0)", "(2,1)"}, 0.5},
			{"slope(point '(1e308,0)',point '(-1e308,2)')", "slope($1::point,$2::point)", []any{"(1e308,0)", "(-1e308,2)"}, []any{"(0,0)", "(2,1)"}, 0.5},
			{"slope(point '(0,0)',point '(0.00001,1e308)')", "slope($1::point,$2::point)", []any{"(0,0)", "(0.00001,1e308)"}, []any{"(0,0)", "(2,1)"}, 0.5},
		} {
			t.Run(tc.expression, func(t *testing.T) {
				for _, prepared := range []bool{false, true} {
					fixture.RunStep(t, p.Ctx, c, b.Recorder, boxError("SELECT "+tc.expression, "22003", "value out of range: overflow", "", "", prepared, true))
				}
				q := "SELECT " + tc.call + " AS v"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				s := boxError(q, "22003", "value out of range: overflow", "", "", false, true, tc.args...)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, held, b.Recorder, geometryMeasureSelect(b, q, []string{"v"}, []string{"FLOAT8"}, [][]any{{tc.want}}, tc.good...))
			})
		}
		for _, tc := range []struct {
			expression, typ string
			want            any
		}{
			{"width(box '(4,1e308),(1,-1e308)')", "FLOAT8", float64(3)},
			{"height(box '(1e308,6),(-1e308,2)')", "FLOAT8", float64(4)},
			{"diagonal(box '(4,1e308),(1,-1e308)')", "LSEG", "[(4,1e+308),(1,-1e+308)]"},
			{"diagonal(box '(1e308,6),(-1e308,2)')", "LSEG", "[(1e+308,6),(-1e+308,2)]"},
		} {
			s := geometryMeasureSelect(b, "SELECT "+tc.expression+" AS v", []string{"v"}, []string{tc.typ}, [][]any{{tc.want}})
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
		}
	})
}
