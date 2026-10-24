package sqltype

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
)

func TestGeometryPositionPredicates(t *testing.T) {
	types := testTypes()
	type positionCase struct {
		id         ID
		name, a, b string
		want       bool
	}
	var cases []positionCase
	for _, shape := range []struct {
		id   ID
		a, b string
	}{
		{Box, "(1,1),(0,0)", "(4,4),(3,3)"},
		{Polygon, "((0,0),(1,0),(1,1),(0,1))", "((3,3),(4,3),(4,4),(3,4))"},
	} {
		for _, direction := range []struct {
			name          string
			forward, self bool
		}{
			{"<<", true, false}, {">>", false, false}, {"&<", true, true}, {"&>", false, true},
			{"<<|", true, false}, {"|>>", false, false}, {"&<|", true, true}, {"|&>", false, true},
			{"<^", true, false}, {">^", false, false},
		} {
			if shape.id == Polygon && (direction.name == "<^" || direction.name == ">^") {
				continue
			}
			cases = append(cases, positionCase{shape.id, direction.name, shape.a, shape.b, direction.forward}, positionCase{shape.id, direction.name, shape.b, shape.a, !direction.forward}, positionCase{shape.id, direction.name, shape.a, shape.a, direction.self})
		}
	}
	cases = append(cases, []positionCase{
		{Box, "<<", "(1,1),(0,0)", "(2,2),(1,1)", false},
		{Box, "<<|", "(1,1),(0,0)", "(2,2),(1,1)", false},
		{Box, "<^", "(1,1),(0,0)", "(2,2),(1,1)", true},
		{Box, ">^", "(2,2),(1,1)", "(1,1),(0,0)", true},
		{Box, "<^", "(2,2),(0,0)", "(3,3),(1,1)", false},
		{Point, "<^", "(1,1)", "(1,1)", false},
		{Point, ">^", "(1,1)", "(1,1)", false},
		{Box, "<<", "(2,2),(0,0)", "(3,3),(1,1)", false},
		{Box, "&<", "(2,2),(0,0)", "(3,3),(1,1)", true},
		{Box, "&>", "(2,2),(0,0)", "(3,3),(1,1)", false},
		{Polygon, "&<", "((0,0),(2,2))", "((1,1),(2,2))", true},
		{Polygon, "&<", "((1,1),(2,2))", "((0,0),(2,2))", true},
		{Polygon, "&>", "((0,0),(2,2))", "((1,1),(2,2))", false},
		{Polygon, "&>", "((1,1),(2,2))", "((0,0),(2,2))", true},
		{Polygon, "<<", "((1,1),(0,1),(0,0),(1,0),(1,1))", "((3,3))", true},
		{Polygon, "<<|", "((1,1),(0,0),(0.5,0.5))", "((3,3))", true},
		{Polygon, "&>", "((0,0),(NaN,0))", "((0,0),(NaN,0))", true},
		{Polygon, "&<", "((0,0),(NaN,0))", "((0,0),(NaN,0))", false},
		{Circle, "&>", "<(0,1e308),1e308>", "<(0,0),0>", false},
		{Box, "&<|", "(2,2),(0,0)", "(2,2),(1,1)", true},
		{Box, "&<|", "(2,2),(1,1)", "(2,2),(0,0)", true},
		{Box, "|&>", "(2,2),(0,0)", "(2,2),(1,1)", false},
		{Box, "|&>", "(2,2),(1,1)", "(2,2),(0,0)", true},
		{Polygon, "&<|", "((0,0),(2,2))", "((1,1),(2,2))", true},
		{Polygon, "&<|", "((1,1),(2,2))", "((0,0),(2,2))", true},
		{Polygon, "|&>", "((0,0),(2,2))", "((1,1),(2,2))", false},
		{Polygon, "|&>", "((1,1),(2,2))", "((0,0),(2,2))", true},
		{Circle, "&<|", "<(0,1),1>", "<(9,1.5),0.5>", true},
		{Circle, "&<|", "<(9,1.5),0.5>", "<(0,1),1>", true},
		{Circle, "|&>", "<(0,1),1>", "<(9,1.5),0.5>", false},
		{Circle, "|&>", "<(9,1.5),0.5>", "<(0,1),1>", true},
		{Polygon, "|&>", "((0,0),(0,NaN))", "((0,0))", true},
		{Polygon, "&<|", "((0,0),(0,NaN))", "((0,0))", false},
		{Circle, "|&>", "<(1e308,0),1e308>", "<(0,0),0>", false},
	}...)
	for _, shape := range []struct {
		id           ID
		zero, format string
		raw          bool
	}{
		{Point, "(0,0)", "(%s,%s)", false},
		{Box, "(0,0),(0,0)", "(%s,%s),(%[1]s,%[2]s)", false},
		{Polygon, "((0,0))", "((%s,%s))", true},
		{Circle, "<(0,0),0>", "<(%s,%s),0>", false},
	} {
		for _, delta := range []struct {
			text string
			far  bool
		}{{"0.0000005", false}, {"0.000001", false}, {"0.000002", true}} {
			b := fmt.Sprintf(shape.format, delta.text, delta.text)
			for _, op := range []string{"<<", "<<|"} {
				cases = append(cases, positionCase{shape.id, op, shape.zero, b, shape.raw || delta.far})
			}
			for _, op := range []string{">>", "|>>"} {
				cases = append(cases, positionCase{shape.id, op, b, shape.zero, shape.raw || delta.far})
			}
		}
		for _, axis := range []struct{ op, ax, ay, bx, by string }{
			{"<<", "0", "NaN", "2", "NaN"}, {"<<|", "NaN", "0", "NaN", "2"},
		} {
			cases = append(cases, positionCase{shape.id, axis.op, fmt.Sprintf(shape.format, axis.ax, axis.ay), fmt.Sprintf(shape.format, axis.bx, axis.by), true})
		}
		for _, special := range []struct {
			text      string
			inclusive bool
		}{{"NaN", false}, {"Infinity", true}, {"-Infinity", true}, {"-0", true}} {
			a := fmt.Sprintf(shape.format, special.text, special.text)
			cases = append(cases, positionCase{shape.id, "<<", a, a, false}, positionCase{shape.id, "<<|", a, a, false})
			if shape.id != Point {
				for _, op := range []string{"&<", "&>", "&<|", "|&>"} {
					cases = append(cases, positionCase{shape.id, op, a, a, special.inclusive})
				}
			}
		}
		if shape.id != Point {
			b := fmt.Sprintf(shape.format, "0.0000005", "0.0000005")
			for _, op := range []string{"&<", "&<|"} {
				cases = append(cases, positionCase{shape.id, op, b, shape.zero, !shape.raw})
			}
			for _, op := range []string{"&>", "|&>"} {
				cases = append(cases, positionCase{shape.id, op, shape.zero, b, !shape.raw})
			}
		}
		if shape.id != Circle {
			cases = append(cases, positionCase{shape.id, "<<", fmt.Sprintf(shape.format, "-1e308", "-1e308"), fmt.Sprintf(shape.format, "1e308", "1e308"), true})
		}
	}
	for i, tc := range cases {
		t.Run(fmt.Sprintf("%03d_%d_%s", i, tc.id, tc.name), func(t *testing.T) {
			values := [2]Value{circleUnitInput(t, types[tc.id], tc.a), circleUnitInput(t, types[tc.id], tc.b)}
			var got Value
			var err error
			switch tc.id {
			case Point:
				op, _ := PointKind(tc.name)
				p, e := BindPoint(op, types[Bool].Usage(), types[Point].Usage(), types[Point].Usage())
				if e != nil {
					t.Fatal(e)
				}
				got, err = p.Evaluate(context.Background(), values)
			case Circle:
				op, _ := CircleKind(tc.name)
				p, e := BindCircle(op, types[Bool].Usage(), types[Circle].Usage(), types[Circle].Usage())
				if e != nil {
					t.Fatal(e)
				}
				got, err = p.Evaluate(context.Background(), values)
			default:
				p := relationUnitPlan(t, types, tc.name, tc.id, tc.id)
				got, err = p.Evaluate(context.Background(), values)
			}
			if err != nil || got.IsNull() || got.Type() != types[Bool] || got.boolean != tc.want {
				t.Fatal(tc, got, err)
			}
		})
	}
	// Raw BOX component replacement must not be undone by predicate evaluation.
	a := circleUnitInput(t, types[Box], "(2,2),(0,0)")
	a.geometry = &geometryPayload{coordinates: [4]float64{-5, -6, 0, 0}}
	for _, op := range []string{"<<", "<<|"} {
		p := relationUnitPlan(t, types, op, Box, Box)
		v, err := p.Evaluate(context.Background(), [2]Value{a, circleUnitInput(t, types[Box], "(0,0),(-1,-1)")})
		if err != nil || !v.boolean || a.geometry.coordinates != ([4]float64{-5, -6, 0, 0}) {
			t.Fatal("raw corners", v, err)
		}
	}
	for _, tc := range []struct{ op, a string }{{"<<", "<(1e308,0),1e308>"}, {"<<|", "<(0,1e308),1e308>"}} {
		op, _ := CircleKind(tc.op)
		p, err := BindCircle(op, types[Bool].Usage(), types[Circle].Usage(), types[Circle].Usage())
		if err != nil {
			t.Fatal(err)
		}
		_, err = p.Evaluate(context.Background(), [2]Value{circleUnitInput(t, types[Circle], tc.a), circleUnitInput(t, types[Circle], "<(0,0),0>")})
		circleUnitError(t, err, "22003", "value out of range: overflow")
	}
}

func TestGeometryPositionPlanAuthority(t *testing.T) {
	types, foreign := testTypes(), testTypes()
	for _, id := range []ID{Box, Polygon} {
		for _, op := range []string{"<<", ">>", "&<", "&>", "<<|", "|>>", "&<|", "|&>", "<^", ">^"} {
			if id == Polygon && (op == "<^" || op == ">^") {
				continue
			}
			p := relationUnitPlan(t, types, op, id, id)
			inputs := [2]Usage{types[id].Usage(), types[id].Usage()}
			for i := range inputs {
				bad := inputs
				bad[i] = foreign[id].Usage()
				if _, err := BindGeometryRelation(p.Operation(), types[Bool].Usage(), bad); err == nil {
					t.Fatal("foreign input admitted", id, op, i)
				}
				bad = inputs
				bad[i].Typmod = 1
				if _, err := BindGeometryRelation(p.Operation(), types[Bool].Usage(), bad); err == nil {
					t.Fatal("modified usage admitted", id, op, i)
				}
				values := [2]Value{Null(types[id]), Null(types[id])}
				values[i] = Null(foreign[id])
				if _, err := p.Evaluate(context.Background(), values); err == nil {
					t.Fatal("foreign NULL hidden", id, op, i)
				}
				values[i] = Value{typ: types[id]}
				if _, err := p.Evaluate(context.Background(), values); err == nil {
					t.Fatal("missing payload hidden by NULL", id, op, i)
				}
			}
			if _, err := BindGeometryRelation(p.Operation(), foreign[Bool].Usage(), inputs); err == nil {
				t.Fatal("foreign result admitted", id, op)
			}
			v, err := p.Evaluate(context.Background(), [2]Value{Null(types[id]), Null(types[id])})
			if err != nil || !v.IsNull() || v.Type() != types[Bool] {
				t.Fatal("typed NULL authority", v, err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if _, err := p.Evaluate(ctx, [2]Value{Null(types[id]), Null(types[id])}); !errors.Is(err, context.Canceled) {
				t.Fatal("NULL hid cancellation", err)
			}
		}
	}
	p := relationUnitPlan(t, types, "<<", Polygon, Polygon)
	values := [2]Value{circleUnitInput(t, types[Polygon], "((0,0),(1,1),(2,2),(3,3))"), circleUnitInput(t, types[Polygon], "((5,5),(6,6))")}
	before := [2][]polygonPoint{slices.Clone(values[0].polygon.points), slices.Clone(values[1].polygon.points)}
	ctx := &measureCancelContext{Context: context.Background(), remaining: 3}
	if v, err := p.Evaluate(ctx, values); !errors.Is(err, context.Canceled) || v != (Value{}) {
		t.Fatal("bounds traversal cancellation", v, err)
	}
	for i := range values {
		if !slices.Equal(values[i].polygon.points, before[i]) {
			t.Fatal("position evaluation mutated input")
		}
	}
	if _, err := BindGeometryRelation(GeometryRelationOperator(255), types[Bool].Usage(), [2]Usage{types[Box].Usage(), types[Box].Usage()}); err == nil {
		t.Fatal("invalid operation admitted")
	}
}
