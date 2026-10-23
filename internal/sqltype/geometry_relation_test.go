package sqltype

import (
	"context"
	"errors"
	"math"
	"testing"
)

func relationUnitPlan(t *testing.T, types map[ID]*Descriptor, name string, left, right ID) GeometryRelationPlan {
	t.Helper()
	op, ok := GeometryRelationKind(name, left, right)
	if !ok {
		t.Fatalf("missing relation %s(%d,%d)", name, left, right)
	}
	p, err := BindGeometryRelation(op, types[op.ResultID()].Usage(), [2]Usage{types[left].Usage(), types[right].Usage()})
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func relationUnitEvaluate(t *testing.T, types map[ID]*Descriptor, name string, left, right ID, a, b string) Value {
	t.Helper()
	p := relationUnitPlan(t, types, name, left, right)
	v, err := p.Evaluate(context.Background(), [2]Value{circleUnitInput(t, types[left], a), circleUnitInput(t, types[right], b)})
	if err != nil {
		t.Fatal(err)
	}
	if v.Type() != types[p.operation.ResultID()] {
		t.Fatal("lost result authority")
	}
	return v
}
func TestGeometryRelationSignatures(t *testing.T) {
	types := testTypes()
	// Actual binding/NULL evaluation exercises result authority across unlike
	// operands and all four result kinds, not inventory/source-count agreement.
	for _, tc := range []struct {
		name                string
		left, right, result ID
	}{
		{"#", Lseg, Lseg, Point}, {"#", Box, Box, Box}, {"##", Line, Lseg, Point},
		{"<->", Point, Path, Float8}, {"<->", Circle, Polygon, Float8},
		{"@>", Polygon, Polygon, Bool}, {"<@", Point, Line, Bool}, {"?#", Line, Box, Bool},
		{"&&", Circle, Circle, Bool}, {"@>", Polygon, Point, Bool},
	} {
		p := relationUnitPlan(t, types, tc.name, tc.left, tc.right)
		v, err := p.Evaluate(context.Background(), [2]Value{Null(types[tc.left]), Null(types[tc.right])})
		if err != nil || !v.IsNull() || v.Type() != types[tc.result] {
			t.Fatal(tc, v, err)
		}
	}
	for _, tc := range []struct {
		name        string
		left, right ID
	}{{"##", Point, Point}, {"#", Lseg, Line}, {"?#", Box, Lseg}, {"##", Lseg, Line}, {"@>", Point, Box}, {"&&", Lseg, Lseg}} {
		if _, ok := GeometryRelationKind(tc.name, tc.left, tc.right); ok {
			t.Fatal("unsupported ordered signature", tc)
		}
	}
}
func TestGeometryRelationLinear(t *testing.T) {
	types := testTypes()
	cross := relationUnitEvaluate(t, types, "#", Lseg, Lseg, "[(0,0),(2,0)]", "[(1,-1),(1,1)]")
	if relationValuePoint(cross) != (relationPoint{1, 0}) || cross.geometry != nil {
		t.Fatal("inline crossing", cross)
	}
	for _, name := range []string{"#", "?#"} {
		v := relationUnitEvaluate(t, types, name, Lseg, Lseg, "[(0,0),(2,0)]", "[(1,0),(3,0)]")
		if name == "#" && !v.IsNull() || name == "?#" && v.boolean {
			t.Fatal("nonunique intersection", v)
		}
	}
	raw := relationUnitEvaluate(t, types, "#", Box, Box, "(2,2),(0,0)", "(4,2),(2.0000005,0)")
	if raw.IsNull() || raw.geometry.coordinates != ([4]float64{2, 2, 2.0000005, 0}) {
		t.Fatal("raw intersection normalized", raw)
	}
	exact := relationUnitEvaluate(t, types, "<@", Point, Box, "(2.0000005,1)", "(2,2),(0,0)")
	if exact.boolean {
		t.Fatal("point box tolerance changed")
	}
	scaled := relationUnitEvaluate(t, types, "<@", Point, Line, "(0,0.0000005)", "{0,100,0}")
	if scaled.boolean {
		t.Fatal("line residual normalized")
	}
	centers := relationUnitEvaluate(t, types, "<->", Box, Box, "(2,2),(0,0)", "(3,2),(1,0)")
	if centers.floating != 1 {
		t.Fatal("box set distance substituted", centers)
	}
	for _, tc := range []struct {
		name          string
		left, right   ID
		a, b, message string
	}{
		{"<->", Point, Point, "(1e308,0)", "(-1e308,0)", "overflow"},
		{"<@", Point, Line, "(1e-200,1)", "{1e-200,1,0}", "underflow"},
	} {
		p := relationUnitPlan(t, types, tc.name, tc.left, tc.right)
		v, err := p.Evaluate(context.Background(), [2]Value{circleUnitInput(t, types[tc.left], tc.a), circleUnitInput(t, types[tc.right], tc.b)})
		circleUnitError(t, err, "22003", "value out of range: "+tc.message)
		if v != (Value{}) {
			t.Fatal("partial checked result", v)
		}
	}
}
func TestGeometryRelationClosest(t *testing.T) {
	types := testTypes()
	for _, tc := range []struct {
		left, right ID
		a, b        string
		want        relationPoint
		null        bool
	}{
		{Point, Lseg, "(0,0)", "[(2,0),(0,2)]", relationPoint{1, 1}, false},
		{Point, Lseg, "(0,0)", "[(2,2),(2,2)]", relationPoint{2, 2}, false},
		{Lseg, Box, "[(0,1),(2,1)]", "(2,2),(0,0)", relationPoint{1, 1}, false},
		{Lseg, Box, "[(0,3),(2,3)]", "(2,2),(0,0)", relationPoint{0, 2}, false},
		{Line, Lseg, "{0,1,0}", "[(0,1),(2,1.0000005)]", relationPoint{}, true},
		{Lseg, Lseg, "[(0,0),(2,0)]", "[(0,3),(2,3)]", relationPoint{}, true},
		{Point, Line, "(NaN,0)", "{0,1,0}", relationPoint{}, true},
	} {
		v := relationUnitEvaluate(t, types, "##", tc.left, tc.right, tc.a, tc.b)
		if v.IsNull() != tc.null || !tc.null && relationValuePoint(v) != tc.want {
			t.Fatal(tc, v)
		}
	}
	// A nonunique representative's exact endpoint tie chooses the second.
	q, _, err := relationSegmentToLine(relationSegment{{0, 1}, {2, 1}}, relationLine{0, 1, 0})
	if err != nil || q != (relationPoint{2, 1}) {
		t.Fatal("endpoint tie", q, err)
	}
	v := relationUnitEvaluate(t, types, "<->", Point, Lseg, "(NaN,0)", "[(0,0),(2,0)]")
	if !math.IsNaN(v.floating) || v.IsNull() {
		t.Fatal("distance NaN suppressed", v)
	}
}
func TestGeometryRelationPathPolygon(t *testing.T) {
	types := testTypes()
	singleton := relationUnitEvaluate(t, types, "<->", Point, Path, "(8,8)", "[(1,1)]")
	if singleton.floating != 0 || singleton.IsNull() {
		t.Fatal("open singleton distance", singleton)
	}
	noEdges := relationUnitEvaluate(t, types, "<->", Path, Path, "[(1,1)]", "[(0,0),(2,0)]")
	if !noEdges.IsNull() {
		t.Fatal("edgeless path pair", noEdges)
	}
	closed := relationUnitEvaluate(t, types, "<->", Point, Path, "(1,1)", "((0,0),(2,0),(2,2),(0,2))")
	if closed.floating != 1 {
		t.Fatal("closed path treated as filled", closed)
	}
	const notch = "((0,0),(4,0),(4,4),(3,4),(3,1),(1,1),(1,4),(0,4))"
	const triangle = "((0.5,3),(3.5,3),(2,0.5))"
	contains := relationUnitEvaluate(t, types, "@>", Polygon, Polygon, notch, triangle)
	overlap := relationUnitEvaluate(t, types, "&&", Polygon, Polygon, notch, triangle)
	if contains.boolean || !overlap.boolean {
		t.Fatal("concave full-edge containment", contains, overlap)
	}
	p := relationUnitPlan(t, types, "@>", Polygon, Polygon)
	a, b := circleUnitInput(t, types[Polygon], notch), circleUnitInput(t, types[Polygon], triangle)
	ctx := &measureCancelContext{Context: context.Background(), remaining: 6}
	v, err := p.Evaluate(ctx, [2]Value{a, b})
	if !errors.Is(err, context.Canceled) || v != (Value{}) {
		t.Fatal("nested traversal cancellation", v, err)
	}
	if a.polygon.points[0] != (polygonPoint{0, 0}) || b.polygon.points[0] != (polygonPoint{.5, 3}) {
		t.Fatal("published input mutated")
	}
}
func TestGeometryRelationClosingEdge(t *testing.T) {
	for _, tc := range []struct {
		polygon []polygonPoint
		point   relationPoint
	}{
		{[]polygonPoint{{0, 0}, {0, 10}, {2, .0000005}}, relationPoint{1, -1}},
		{[]polygonPoint{{0, 0}, {10, 0}, {.0000005, 2}}, relationPoint{-1, 1}},
	} {
		got, err := relationPolygonPointDistance(context.Background(), tc.polygon, tc.point)
		if err != nil || got != 1 {
			t.Fatal("point distance lost first-vertex closing anchor", got, err)
		}
		// Other polygon traversals intentionally retain last -> first. Its
		// different numerical result makes an accidental global reversal visible.
		_, reverse, err := relationSegmentPoint(relationPolygonEdge(tc.polygon, 0), tc.point)
		if err != nil || reverse != 1.0000005 {
			t.Fatal("generic closing traversal changed", reverse, err)
		}
	}
}
func TestGeometryRelationAuthority(t *testing.T) {
	types, foreign := testTypes(), testTypes()
	for _, tc := range []struct {
		name        string
		left, right ID
	}{{"##", Point, Lseg}, {"#", Box, Box}, {"<@", Point, Line}, {"<->", Polygon, Circle}} {
		p := relationUnitPlan(t, types, tc.name, tc.left, tc.right)
		inputs := [2]Usage{types[tc.left].Usage(), types[tc.right].Usage()}
		for i, id := range [2]ID{tc.left, tc.right} {
			bad := inputs
			bad[i] = foreign[id].Usage()
			if _, err := BindGeometryRelation(p.operation, types[p.operation.ResultID()].Usage(), bad); err == nil {
				t.Fatal("foreign input", tc, i)
			}
			bad = inputs
			bad[i].Typmod = 1
			if _, err := BindGeometryRelation(p.operation, types[p.operation.ResultID()].Usage(), bad); err == nil {
				t.Fatal("modified usage", tc, i)
			}
			values := [2]Value{Null(types[tc.left]), Null(types[tc.right])}
			values[i] = Null(foreign[id])
			if _, err := p.Evaluate(context.Background(), values); err == nil {
				t.Fatal("foreign NULL", tc, i)
			}
		}
		if _, err := BindGeometryRelation(p.operation, foreign[p.operation.ResultID()].Usage(), inputs); err == nil {
			t.Fatal("foreign result", tc)
		}
	}
	p := relationUnitPlan(t, types, "##", Point, Lseg)
	if _, err := p.Evaluate(context.Background(), [2]Value{Null(types[Point]), {typ: types[Lseg]}}); err == nil {
		t.Fatal("missing payload hidden")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Evaluate(ctx, [2]Value{Null(types[Point]), Null(types[Lseg])}); !errors.Is(err, context.Canceled) {
		t.Fatal("NULL hid cancellation", err)
	}
	if _, err := (GeometryRelationPlan{}).Evaluate(context.Background(), [2]Value{}); err == nil {
		t.Fatal("zero plan accepted")
	}
}
