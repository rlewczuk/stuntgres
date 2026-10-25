package reference_test

import (
	"database/sql"
	"strconv"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceGeometryConversionValues(t *testing.T) {
	geometryConversionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range geometryConversionCases {
			t.Run(tc.name, func(t *testing.T) {
				s := geometryConversionSelect(b, "SELECT "+tc.expression+" AS v", []string{"v"}, []string{tc.typ}, [][]any{{tc.want}})
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
			})
		}
		for _, tc := range []struct{ expr, typ, want string }{
			{"box(point '(7,-2)',point '(-3,5)')", "BOX", "(7,5),(-3,-2)"},
			{"box(point '(-3,5)',point '(7,-2)')", "BOX", "(7,5),(-3,-2)"},
			{"box(polygon '((2,3))')", "BOX", "(2,3),(2,3)"},
			{"circle(box '(2,3),(2,3)')", "CIRCLE", "<(2,3),0>"},
			{"circle(polygon '((0,0),(0,0),(6,0))')", "CIRCLE", "<(2,0),2.6666666666666665>"},
			{"circle(polygon '((2,3))')", "CIRCLE", "<(2,3),0>"},
			{"line(point '(2,1)',point '(2,9)')", "LINE", "{-1,0,2}"},
			{"line(point '(1,3)',point '(3,7)')", "LINE", "{2,-1,1}"},
			{"line(point '(3,7)',point '(1,3)')", "LINE", "{2,-1,1}"},
			{"circle(point '(0,0)',-1)", "CIRCLE", "<(0,0),-1>"},
			{"circle(point '(0,0)','-Infinity'::float8)", "CIRCLE", "<(0,0),-Infinity>"},
			{"circle(point '(0,0)','Infinity'::float8)", "CIRCLE", "<(0,0),Infinity>"},
			{"circle(point '(0,0)','NaN'::float8)", "CIRCLE", "<(0,0),NaN>"},
			{"circle(point '(0,0)','-0'::float8)", "CIRCLE", "<(0,0),-0>"},
			{"box(circle(point '(0,0)',-1))", "BOX", "(-0.7071067811865475,-0.7071067811865475),(0.7071067811865475,0.7071067811865475)"},
			{"circle(box(circle(point '(0,0)',-1)))", "CIRCLE", "<(0,0),1>"},
			{"bound_box(box(circle(point '(0,0)',-1)),box(point '(0,0)'))", "BOX", "(0,0),(0,0)"},
			{"box(point '(NaN,1)',point '(2,NaN)')", "BOX", "(NaN,NaN),(2,1)"},
			{"box(point '(0,-0)',point '(-0,0)')", "BOX", "(-0,0),(0,-0)"},
			{"box('(2,3),(0,0)')", "BOX", "(2,3),(0,0)"},
			{"box('(2,3),(0,0)'::text)", "BOX", "(2,3),(0,0)"},
			{"box(box '(2,3),(0,0)')", "BOX", "(2,3),(0,0)"},
			{"circle('<(2,3),4>'::circle)", "CIRCLE", "<(2,3),4>"},
		} {
			t.Run(tc.expr, func(t *testing.T) {
				s := geometryConversionSelect(b, "SELECT "+tc.expr+" AS v", []string{"v"}, []string{tc.typ}, [][]any{{tc.want}})
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
			})
		}
		for _, expr := range []string{"box(NULL)", "circle(NULL)"} {
			typ := "BOX"
			if expr == "circle(NULL)" {
				typ = "CIRCLE"
			}
			s := geometryConversionSelect(b, "SELECT "+expr+" AS v", []string{"v"}, []string{typ}, [][]any{{nil}})
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
		}
		for _, tc := range []struct {
			expr, typ string
			want      any
		}{
			{"radius(circle(point '(0,0)',-1))", "FLOAT8", float64(-1)},
			{"circle(point '(0,0)',-1) ~= '<(0,0),1>'::circle", "BOOL", false},
			{"circle(point '(0,0)',-1) = '<(0,0),1>'::circle", "BOOL", true},
			{"circle(point '(0,0)',-1) <-> '<(3,0),1>'::circle", "FLOAT8", float64(3)},
			{"circle(point '(0,0)',-1) @> '<(0,0),0>'::circle", "BOOL", false},
		} {
			t.Run(tc.expr, func(t *testing.T) {
				s := fixture.Select("SELECT "+tc.expr+" AS v", []string{"v"}, []string{tc.typ}, [][]any{{tc.want}})
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
			})
		}
	})
}

func TestReferenceGeometryConversionBindingsMetadata(t *testing.T) {
	geometryConversionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range geometryConversionCases {
			t.Run(tc.name, func(t *testing.T) {
				q := "SELECT " + tc.parameter + " AS v"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				check := func(args []any, want any) {
					s := geometryConversionSelect(b, q, []string{"v"}, []string{tc.typ}, [][]any{{want}}, args...)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				}
				check(tc.args, tc.want)
				for i := range tc.args {
					args := append([]any(nil), tc.args...)
					args[i] = nil
					check(args, nil)
					if _, geometryText := tc.args[i].(string); geometryText {
						args[i] = []byte(nil)
						check(args, nil)
					}
				}
				args := append([]any(nil), tc.args...)
				for i, a := range args {
					switch a := a.(type) {
					case string:
						args[i] = []byte(a)
					case float64:
						args[i] = []byte(strconv.FormatFloat(a, 'g', -1, 64))
					}
				}
				check(args, tc.want)
				rows, err := stmt.QueryContext(p.Ctx, args...)
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				retained := geometryConversionSelect(b, q, []string{"v"}, []string{tc.typ}, [][]any{{tc.want}})
				retained.CheckMetadata(t, rows)
				for _, arg := range args {
					if buffer, ok := arg.([]byte); ok {
						for i := range buffer {
							buffer[i] = '?'
						}
					}
				}
				if !rows.Next() {
					t.Fatalf("retained result: %v", rows.Err())
				}
				var value any
				if err := rows.Scan(&value); err != nil {
					t.Fatal(err)
				}
				retained.CheckRaw(t, 0, 0, value)
				if got := fixture.Normalize(value); got != tc.want {
					t.Fatalf("caller bytes mutated retained value: %v want %s", got, tc.want)
				}
				if rows.Next() || rows.Err() != nil {
					t.Fatalf("retained exhaustion: %v", rows.Err())
				}
				rows.Close()
				check(tc.args, tc.want)
				s := geometryConversionSelect(b, q+" WHERE false", []string{"v"}, []string{tc.typ}, nil, tc.args...)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
			})
		}
	})
}
