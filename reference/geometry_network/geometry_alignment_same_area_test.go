package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceGeometrySamenessArea(t *testing.T) {
	for _, tc := range []struct {
		name, typ, a, z string
		want            bool
	}{
		{"box_nan_same", "box", "(NaN,1),(0,0)", "(NaN,1),(0,0)", true},
		{"box_nan_corner_exact", "box", "(NaN,1),(0,0)", "(NaN,1.0000005),(0,0)", false},
		{"box_other_corner_fuzzy", "box", "(NaN,1),(0,0)", "(NaN,1),(0,0.0000005)", true},
		{"polygon_later_candidate", "polygon", "((0,0),(1,0),(0,0),(0,1),(2,2))", "((0,0),(0,1),(2,2),(0,0),(1,0))", true},
		{"circle_nan_center_exact", "circle", "<(NaN,1),2>", "<(NaN,1.0000005),2>", false},
		{"box_huge_same", "box", "(1e200,1e200),(0,0)", "(1e200,1e200),(0,0)", true},
		{"box_tiny_same", "box", "(1e-200,1e-200),(0,0)", "(1e-200,1e-200),(0,0)", true},
		{"circle_huge_same", "circle", "<(0,0),1e200>", "<(0,0),1e200>", true},
		{"circle_tiny_same", "circle", "<(0,0),1e-200>", "<(0,0),1e-200>", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			geometryAlignmentScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				q := "SELECT $1::" + tc.typ + " ~= $2::" + tc.typ + " AS v"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				for _, sample := range []struct {
					args []any
					want any
				}{{[]any{tc.a, tc.z}, tc.want}, {[]any{nil, tc.z}, nil}, {[]any{tc.a, tc.z}, tc.want}} {
					s := boxSelect(b, q, []string{"v"}, []string{"BOOL"}, [][]any{{sample.want}}, sample.args...)
					run(s)
					run(fixture.Prepared(s))
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				}
				s := boxSelect(b, "SELECT "+boxQuote(tc.a)+"::"+tc.typ+" ~= "+boxQuote(tc.z)+"::"+tc.typ+" AS v", []string{"v"}, []string{"BOOL"}, [][]any{{tc.want}})
				run(s)
				run(fixture.Prepared(s))
			})
		})
	}
	t.Run("area_edges", func(t *testing.T) {
		geometryAlignmentScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			for _, tc := range []struct {
				expr string
				want bool
			}{
				{"box '(0,1),(0,0)' = box '(0.000001,1),(0,0)'", true},
				{"box '(0.000001,1),(0,0)' = box '(0.000002,1),(0,0)'", true},
				{"box '(0,1),(0,0)' = box '(0.000002,1),(0,0)'", false},
				{"circle '<(0,0),Infinity>' = circle '<(9,9),Infinity>'", true},
				{"circle '<(0,0),Infinity>' <> circle '<(9,9),Infinity>'", false},
				{"circle '<(0,0),Infinity>' < circle '<(9,9),Infinity>'", false},
				{"circle '<(0,0),Infinity>' > circle '<(9,9),Infinity>'", false},
				{"circle '<(0,0),Infinity>' <= circle '<(9,9),Infinity>'", true},
				{"circle '<(0,0),Infinity>' >= circle '<(9,9),Infinity>'", true},
			} {
				s := boxSelect(b, "SELECT "+tc.expr+" AS v", []string{"v"}, []string{"BOOL"}, [][]any{{tc.want}})
				run(s)
				run(fixture.Prepared(s))
			}
			for _, tc := range []struct{ typ, a, message string }{
				{"box", "(1e200,1e200),(0,0)", "overflow"}, {"box", "(1e-200,1e-200),(0,0)", "underflow"},
				{"circle", "<(0,0),1e200>", "overflow"}, {"circle", "<(0,0),1e-200>", "underflow"},
			} {
				q := "SELECT $1::" + tc.typ + " = $2::" + tc.typ
				s := boxError(q, "22003", "value out of range: "+tc.message, "", "", false, true, tc.a, tc.a)
				run(s)
				run(fixture.Prepared(s))
				q = "SELECT " + boxQuote(tc.a) + "::" + tc.typ + " = " + boxQuote(tc.a) + "::" + tc.typ
				for _, prepared := range []bool{false, true} {
					run(boxError(q, "22003", "value out of range: "+tc.message, "", "", prepared, true))
				}
			}
			run(fixture.Exec("CREATE TABLE geom_alignment_raw(b box)", 0))
			run(fixture.Exec("INSERT INTO geom_alignment_raw VALUES('(2,2),(0,0)')", 1))
			run(fixture.Exec("UPDATE geom_alignment_raw SET b[0]=point '(-1,-1)'", 1))
			s := boxSelect(b, "SELECT b ~= box '(0,0),(-1,-1)' AS v FROM geom_alignment_raw", []string{"v"}, []string{"BOOL"}, [][]any{{false}})
			run(s)
			run(fixture.Prepared(s))
		})
	})
}
