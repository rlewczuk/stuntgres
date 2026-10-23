package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceGeometryPositionBoundaries(t *testing.T) {
	cases := []geometryRelationCase{
		{"<<", "box", "box", "(1,1),(0,0)", "(2,2),(1,1)", "BOOL", false},
		{"<<|", "box", "box", "(1,1),(0,0)", "(2,2),(1,1)", "BOOL", false},
		{"<^", "box", "box", "(1,1),(0,0)", "(2,2),(1,1)", "BOOL", true},
		{">^", "box", "box", "(2,2),(1,1)", "(1,1),(0,0)", "BOOL", true},
		{"&<", "box", "box", "(1,1),(0,0)", "(2,2),(1,1)", "BOOL", true},
		{"&<|", "box", "box", "(1,1),(0,0)", "(2,2),(1,1)", "BOOL", true},
		{"<^", "point", "point", "(1,1)", "(1,1)", "BOOL", false},
		{">^", "point", "point", "(1,1)", "(1,1)", "BOOL", false},
		{"<<", "polygon", "polygon", "((0,0),(1,1))", "((1,1),(2,2))", "BOOL", false},
		{"&<|", "polygon", "polygon", "((0,0),(1,1))", "((1,1),(2,2))", "BOOL", true},
		{"<<", "circle", "circle", "<(0,0),1>", "<(2,2),1>", "BOOL", false},
		{"&<|", "circle", "circle", "<(0,0),1>", "<(2,2),1>", "BOOL", true},
		{"<^", "box", "box", "(2,2),(0,0)", "(3,3),(1,1)", "BOOL", false},
		{"&<", "box", "box", "(2,2),(0,0)", "(2,2),(1,1)", "BOOL", true},
		{"&<", "box", "box", "(2,2),(1,1)", "(2,2),(0,0)", "BOOL", true},
		{"&>", "box", "box", "(2,2),(0,0)", "(2,2),(1,1)", "BOOL", false},
		{"&>", "box", "box", "(2,2),(1,1)", "(2,2),(0,0)", "BOOL", true},
		{"&<", "polygon", "polygon", "((0,0),(2,2))", "((1,1),(2,2))", "BOOL", true},
		{"&<", "polygon", "polygon", "((1,1),(2,2))", "((0,0),(2,2))", "BOOL", true},
		{"&>", "polygon", "polygon", "((0,0),(2,2))", "((1,1),(2,2))", "BOOL", false},
		{"&>", "polygon", "polygon", "((1,1),(2,2))", "((0,0),(2,2))", "BOOL", true},
		{"<<", "point", "point", "(-0,-0)", "(0,0)", "BOOL", false},
		{"&<", "box", "box", "(-0,-0),(-0,-0)", "(0,0),(0,0)", "BOOL", true},
		{"&<|", "polygon", "polygon", "((-0,-0))", "((0,0))", "BOOL", true},
		{"|&>", "circle", "circle", "<(-0,-0),0>", "<(0,0),0>", "BOOL", true},
		{"&>", "polygon", "polygon", "((0,0),(NaN,0))", "((0,0),(NaN,0))", "BOOL", true},
		{"&<", "polygon", "polygon", "((0,0),(NaN,0))", "((0,0),(NaN,0))", "BOOL", false},
		{"<<", "polygon", "polygon", "((1,1),(0,1),(0,0),(1,0),(1,1))", "((3,3))", "BOOL", true},
		{"<<|", "polygon", "polygon", "((1,1),(0,0),(0.5,0.5))", "((3,3))", "BOOL", true},
		{"&>", "circle", "circle", "<(0,1e308),1e308>", "<(0,0),0>", "BOOL", false},
	}
	// Overlap and equal endpoints compare like extents, not opposing edges.
	for _, shape := range []struct{ typ, a, b string }{
		{"box", "(2,2),(0,0)", "(3,3),(1,1)"},
		{"polygon", "((0,0),(2,0),(2,2),(0,2))", "((1,1),(3,1),(3,3),(1,3))"},
		{"circle", "<(1,1),1>", "<(2,2),1>"},
	} {
		for _, v := range []struct {
			op         string
			swap, want bool
		}{
			{"<<", false, false}, {"&<", false, true}, {"&>", true, true}, {"&>", false, false},
			{"<<|", false, false}, {"&<|", false, true}, {"|&>", true, true}, {"|&>", false, false},
		} {
			a, b := shape.a, shape.b
			if v.swap {
				a, b = b, a
			}
			cases = append(cases, geometryRelationCase{v.op, shape.typ, shape.typ, a, b, "BOOL", v.want})
		}
	}
	// Released polygon predicates use raw IEEE comparisons; the other three
	// families use EPSILON=1e-6. Wants are independent literal thresholds.
	for _, shape := range []struct {
		typ, zero, format string
		near              bool
	}{
		{"point", "(0,0)", "(%s,%s)", false},
		{"box", "(0,0),(0,0)", "(%s,%s),(%[1]s,%[2]s)", false},
		{"polygon", "((0,0))", "((%s,%s))", true},
		{"circle", "<(0,0),0>", "<(%s,%s),0>", false},
	} {
		for _, delta := range []struct {
			text string
			far  bool
		}{{"0.0000005", false}, {"0.000001", false}, {"0.000002", true}} {
			peer := fmt.Sprintf(shape.format, delta.text, delta.text)
			for _, op := range []string{"<<", "<<|"} {
				cases = append(cases, geometryRelationCase{op, shape.typ, shape.typ, shape.zero, peer, "BOOL", shape.near || delta.far})
			}
			for _, op := range []string{">>", "|>>"} {
				cases = append(cases, geometryRelationCase{op, shape.typ, shape.typ, peer, shape.zero, "BOOL", shape.near || delta.far})
			}
		}
		if shape.typ != "point" {
			peer := fmt.Sprintf(shape.format, "0.0000005", "0.0000005")
			for _, op := range []string{"&<", "&<|"} {
				cases = append(cases, geometryRelationCase{op, shape.typ, shape.typ, peer, shape.zero, "BOOL", !shape.near})
			}
			for _, op := range []string{"&>", "|&>"} {
				cases = append(cases, geometryRelationCase{op, shape.typ, shape.typ, shape.zero, peer, "BOOL", !shape.near})
			}
		}
		for _, special := range []struct {
			text      string
			inclusive bool
		}{{"NaN", false}, {"Infinity", true}, {"-Infinity", true}, {"-0", true}} {
			a := fmt.Sprintf(shape.format, special.text, special.text)
			for _, direction := range geometryPositionDirections {
				if !geometryPositionAdmitted(shape.typ, direction.op) {
					continue
				}
				want := direction.self && special.inclusive
				// Degenerate BOX aliases include touching equal endpoints.
				if shape.typ == "box" && (direction.op == "<^" || direction.op == ">^") {
					want = special.inclusive
				}
				cases = append(cases, geometryRelationCase{direction.op, shape.typ, shape.typ, a, a, "BOOL", want})
			}
		}
		for _, axis := range []struct{ op, ax, ay, bx, by string }{
			{"<<", "0", "NaN", "2", "NaN"}, {"<<|", "NaN", "0", "NaN", "2"},
		} {
			cases = append(cases, geometryRelationCase{axis.op, shape.typ, shape.typ, fmt.Sprintf(shape.format, axis.ax, axis.ay), fmt.Sprintf(shape.format, axis.bx, axis.by), "BOOL", true})
		}
		if shape.typ != "circle" {
			cases = append(cases, geometryRelationCase{"<<", shape.typ, shape.typ, fmt.Sprintf(shape.format, "-1e308", "-1e308"), fmt.Sprintf(shape.format, "1e308", "1e308"), "BOOL", true})
		}
	}
	for i, tc := range cases {
		t.Run(fmt.Sprintf("%03d_%s_%s", i, tc.left, tc.op), func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
					q := "SELECT $1::" + tc.left + " " + tc.op + " $2::" + tc.right + " AS v"
					s := fixture.Select(q, []string{"v"}, []string{"BOOL"}, [][]any{{tc.want}}, tc.a, tc.b)
					run(s)
					run(fixture.Prepared(s))
				})
			}
		})
	}
	t.Run("raw_box_and_checked_circle", func(t *testing.T) {
		p := fixture.NewScenario(t)
		for _, backend := range p.Backends() {
			t.Run(backend.Name, func(t *testing.T) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
				run(fixture.Exec("CREATE TABLE position_raw(b box)", 0))
				run(fixture.Exec("INSERT INTO position_raw VALUES('(2,2),(0,0)')", 1))
				run(fixture.Exec("UPDATE position_raw SET b[0]='(-5,-6)'::point", 1))
				s := boxSelect(backend, "SELECT b,b << box '(0,0),(-1,-1)' AS horizontal,b <<| box '(0,0),(-1,-1)' AS vertical FROM position_raw", []string{"b", "horizontal", "vertical"}, []string{"BOX", "BOOL", "BOOL"}, [][]any{{"(-5,-6),(0,0)", true, true}})
				run(s)
				run(fixture.Prepared(s))
				for _, v := range []struct{ a, op string }{{"<(1e308,0),1e308>", "<<"}, {"<(0,1e308),1e308>", "<<|"}} {
					for _, prepared := range []bool{false, true} {
						run(boxError("SELECT $1::circle "+v.op+" $2::circle", "22003", "value out of range: overflow", "", "", prepared, true, v.a, "<(0,0),0>"))
					}
				}
			})
		}
	})
}
