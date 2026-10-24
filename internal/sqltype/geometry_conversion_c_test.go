package sqltype

import (
	"context"
	"errors"
	"math"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestGeometryConversionCEdges(t *testing.T) {
	types, ctx := testTypes(), context.Background()
	bits := [4]float64{math.Copysign(0, -1), math.SmallestNonzeroFloat64, math.Float64frombits(0xfff8000000000042), -3}
	box := Value{typ: types[Box], geometry: &geometryPayload{coordinates: bits}}
	original := box.geometry.coordinates
	got, err := conversionUnitPlan(t, types, PolygonBox).Evaluate(ctx, [2]Value{box})
	if err != nil {
		t.Fatal(err)
	}
	want := []polygonPoint{{bits[2], bits[3]}, {bits[2], bits[1]}, {bits[0], bits[1]}, {bits[0], bits[3]}}
	assertVertices := func(got Value, want []polygonPoint) {
		t.Helper()
		if got.typ != types[Polygon] || !got.validPolygon() || len(got.polygon.points) != len(want) {
			t.Fatal("polygon count/type")
		}
		for i, p := range want {
			q := got.polygon.points[i]
			if math.Float64bits(p.x) != math.Float64bits(q.x) || math.Float64bits(p.y) != math.Float64bits(q.y) {
				t.Fatalf("vertex %d bits/order", i)
			}
		}
	}
	assertVertices(got, want)
	for i, f := range original {
		if math.Float64bits(box.geometry.coordinates[i]) != math.Float64bits(f) {
			t.Fatal("BOX mutated")
		}
	}
	inverted := Value{typ: types[Box], geometry: &geometryPayload{coordinates: [4]float64{-2, -3, 4, 5}}}
	got, err = conversionUnitPlan(t, types, PolygonBox).Evaluate(ctx, [2]Value{inverted})
	if err != nil {
		t.Fatal(err)
	}
	assertVertices(got, []polygonPoint{{4, 5}, {4, -3}, {-2, -3}, {-2, 5}})
	for _, points := range [][][2]float64{
		{{bits[0], bits[2]}}, {{0, 0}, {1, 1}}, {{0, 0}, {0, 0}, {6, 0}}, {{0, 0}, {1, 1}, {0, 0}}, {{bits[0], bits[1]}, {bits[2], bits[3]}},
	} {
		path := Value{typ: types[Path], path: &pathPayload{closed: true, points: points}}
		want := make([]polygonPoint, len(points))
		for i, p := range points {
			want[i] = polygonPoint{p[0], p[1]}
		}
		got, err := conversionUnitPlan(t, types, PolygonPath).Evaluate(ctx, [2]Value{path})
		if err != nil {
			t.Fatal(err)
		}
		assertVertices(got, want)
		for i, p := range path.path.points {
			if math.Float64bits(p[0]) != math.Float64bits(want[i].x) || math.Float64bits(p[1]) != math.Float64bits(want[i].y) {
				t.Fatal("PATH mutated")
			}
		}
	}
	open := Value{typ: types[Path], path: &pathPayload{closed: false, points: [][2]float64{{0, 0}, {1, 1}, {0, 0}}}}
	_, err = conversionUnitPlan(t, types, PolygonPath).Evaluate(ctx, [2]Value{open})
	circleUnitError(t, err, "22023", "open path cannot be converted to polygon")
	circlePlan := conversionUnitPlan(t, types, PolygonCirclePoints)
	circleCall := func(n int64, c circleDatum) (Value, error) {
		return circlePlan.Evaluate(ctx, [2]Value{{typ: types[Int4], integer: n}, {typ: types[Circle], circle: c}})
	}
	for _, n := range []int64{2, 3, 4, 5, 12} {
		v, err := circleCall(n, circleDatum{radius: 1})
		if err != nil || len(v.polygon.points) != int(n) {
			t.Fatal("circle count", n, err)
		}
		first := v.polygon.points[0]
		if first.x != -1 || first.y != 0 {
			t.Fatal("first vertex")
		}
		// Independent geometric witnesses, not the implementation's trig formula:
		// leftmost start, upper-half traversal, unit radius, exact n4 axis order.
		if n > 2 && v.polygon.points[1].y <= 0 {
			t.Fatal("upper half-plane traversal")
		}
		for _, p := range v.polygon.points {
			if math.Abs(math.Hypot(p.x, p.y)-1) > 1e-14 {
				t.Fatal("not on circle")
			}
		}
		if n == 4 {
			for i, xy := range [][2]float64{{-1, 0}, {0, 1}, {1, 0}, {0, -1}} {
				p := v.polygon.points[i]
				if math.Abs(p.x-xy[0]) > 1e-14 || math.Abs(p.y-xy[1]) > 1e-14 {
					t.Fatal("four-point order")
				}
			}
			if v.polygon.points[1].x == 0 || v.polygon.points[2].y == 0 || v.polygon.points[3].x == 0 {
				t.Fatal("trig residual snapped")
			}
		}
	}
	for _, c := range []circleDatum{{x: 2, y: 3, radius: 2}, {radius: -1}} {
		v, err := circleCall(4, c)
		if err != nil || v.polygon.points[0].x != c.x-c.radius || v.polygon.points[0].y != c.y {
			t.Fatal("center/negative radius", v, err)
		}
	}
	for _, endpoint := range []float64{1e-6, -1e-6} {
		for _, radius := range []float64{endpoint, math.Nextafter(endpoint, 0), math.Copysign(0, endpoint)} {
			for _, n := range []int64{0, 4, math.MaxInt32} {
				_, err := circleCall(n, circleDatum{radius: radius})
				circleUnitError(t, err, "0A000", "cannot convert circle with radius zero to polygon")
			}
		}
		radius := math.Nextafter(endpoint, math.Copysign(math.Inf(1), endpoint))
		_, err := circleCall(0, circleDatum{radius: radius})
		circleUnitError(t, err, "22023", "must request at least 2 points")
		if _, err := circleCall(2, circleDatum{radius: radius}); err != nil {
			t.Fatal("epsilon exterior rejected", err)
		}
	}
	for _, radius := range []float64{1, math.NaN(), math.Inf(1), math.Inf(-1)} {
		for _, n := range []int64{-1, 0, 1} {
			_, err := circleCall(n, circleDatum{radius: radius})
			circleUnitError(t, err, "22023", "must request at least 2 points")
		}
		_, err := circleCall(math.MaxInt32, circleDatum{radius: radius})
		circleUnitError(t, err, "54000", "too many points requested")
	}
	_, err = circleCall(2, circleDatum{x: 1e308, radius: 1e308})
	circleUnitError(t, err, "22003", "value out of range: overflow")
	for _, radius := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		v, err := circleCall(2, circleDatum{radius: radius})
		if err != nil || !math.IsNaN(v.polygon.points[0].y) {
			t.Fatal("nonfinite propagation", err)
		}
		if math.IsNaN(radius) {
			if !math.IsNaN(v.polygon.points[0].x) {
				t.Fatal("NaN x")
			}
		} else if v.polygon.points[0].x != -radius {
			t.Fatal("infinite x sign")
		}
	}
	_, err = conversionUnitPlan(t, types, PolygonCircle).Evaluate(ctx, [2]Value{{typ: types[Circle], circle: circleDatum{}}})
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Context != "SQL function \"polygon\" statement 1" {
		t.Fatal("unary wrapper context", err)
	}
	for _, tc := range []struct {
		op    GeometryConversion
		value Value
	}{
		{PolygonBox, Value{typ: types[Box]}},
		{PolygonPath, Value{typ: types[Path]}},
		{PolygonPath, Value{typ: types[Path], path: &pathPayload{closed: true}}},
	} {
		v, err := conversionUnitPlan(t, types, tc.op).Evaluate(ctx, [2]Value{tc.value})
		var state interface{ SQLState() string }
		if err == nil || errors.As(err, &state) || v != (Value{}) {
			t.Fatal("malformed payload must fail invariant", err)
		}
	}
	for _, tc := range []struct {
		op     GeometryConversion
		values [2]Value
	}{
		{PolygonPath, [2]Value{{typ: types[Path], path: &pathPayload{closed: true, points: [][2]float64{{0, 0}, {1, 1}, {2, 0}}}}}},
		{PolygonCirclePoints, [2]Value{{typ: types[Int4], integer: 12}, {typ: types[Circle], circle: circleDatum{radius: 1}}}},
	} {
		cancel := &arrayOrderCancelContext{Context: ctx, remaining: 3}
		v, err := conversionUnitPlan(t, types, tc.op).Evaluate(cancel, tc.values)
		if !errors.Is(err, context.Canceled) || v != (Value{}) {
			t.Fatal("loop published canceled result", err)
		}
	}
}

func TestGeometryConversionCAuthority(t *testing.T) {
	types, foreign := testTypes(), testTypes()
	for _, op := range []GeometryConversion{PolygonBox, PolygonCircle, PolygonCirclePoints, PolygonPath} {
		var inputs [2]Usage
		for i := range op.Arity() {
			inputs[i] = types[op.Formal(i)].Usage()
		}
		result := types[Polygon].Usage()
		for i := range op.Arity() {
			bad := inputs
			bad[i] = foreign[op.Formal(i)].Usage()
			if _, err := BindGeometryConversion(op, result, bad); err == nil {
				t.Fatal("foreign formal bound", op, i)
			}
			bad = inputs
			bad[i].Typmod = 1
			if _, err := BindGeometryConversion(op, result, bad); err == nil {
				t.Fatal("typmod bound")
			}
			bad = inputs
			bad[i].Collation.ID = 100
			if _, err := BindGeometryConversion(op, result, bad); err == nil {
				t.Fatal("collation bound")
			}
		}
		if _, err := BindGeometryConversion(op, foreign[Polygon].Usage(), inputs); err == nil {
			t.Fatal("foreign result bound")
		}
		for _, badResult := range []Usage{{}, {Type: types[Polygon], Typmod: 4}} {
			if _, err := BindGeometryConversion(op, badResult, inputs); err == nil {
				t.Fatal("invalid result bound")
			}
		}
	}
	p := conversionUnitPlan(t, types, PolygonCirclePoints)
	for _, args := range [][2]Value{
		{Null(foreign[Int4]), Null(types[Circle])},
		{Null(types[Int4]), Null(foreign[Circle])},
	} {
		if _, err := p.Evaluate(context.Background(), args); err == nil {
			t.Fatal("foreign typed NULL hidden by strictness")
		}
	}
	v, err := p.Evaluate(context.Background(), [2]Value{Null(types[Int4]), Null(types[Circle])})
	if err != nil || !v.null || v.typ != types[Polygon] {
		t.Fatal("canonical strict result", err)
	}
	for _, missing := range []bool{true, false} {
		copy := *types[Polygon]
		if missing {
			copy.polygonInt = nil
		} else {
			copy.polygonInt = foreign[Int4]
		}
		if _, err := BindGeometryConversion(PolygonCirclePoints, copy.Usage(), [2]Usage{types[Int4].Usage(), types[Circle].Usage()}); err == nil {
			t.Fatal("INT4 authority missing/foreign")
		}
	}
	t.Run("known_text_input", func(t *testing.T) {
		ctx := context.Background()
		p, err := PlanCoercion(types[Text].Usage(), types[Polygon].Usage(), Explicit)
		if err != nil || !p.FunctionCastShortcut() {
			t.Fatal("missing polygon typename input", err)
		}
		got, err := p.Apply(ctx, String(types[Text], "((1,2),(3,4))"), InputEnvironment{}, OutputEnvironment{})
		if err != nil {
			t.Fatal(err)
		}
		if out, err := types[Polygon].Output(ctx, got); err != nil || out != "((1,2),(3,4))" {
			t.Fatal("ordinary polygon codec", out, err)
		}
		got, err = p.Apply(ctx, Null(types[Text]), InputEnvironment{}, OutputEnvironment{})
		if err != nil || !got.IsNull() || got.Type() != types[Polygon] {
			t.Fatal("canonical polygon input NULL", err)
		}
		_, err = p.Apply(ctx, String(types[Text], "bad"), InputEnvironment{}, OutputEnvironment{})
		circleUnitError(t, err, "22P02", "invalid input syntax for type polygon: \"bad\"")
		for _, mode := range []CoercionContext{Implicit, Assignment} {
			if _, err := PlanCoercion(types[Text].Usage(), types[Polygon].Usage(), mode); err == nil {
				t.Fatal("polygon input widened coercion context", mode)
			}
		}
		for _, pair := range [][2]ID{{Point, Polygon}, {Float8, Polygon}} {
			if _, err := PlanCoercion(types[pair[0]].Usage(), types[pair[1]].Usage(), Explicit); err == nil {
				t.Fatal("polygon input widened adjacent edge", pair)
			}
		}
		for _, pair := range [][2]ID{{Polygon, Text}, {VarChar, Polygon}} {
			if _, err := PlanCoercion(types[pair[0]].Usage(), types[pair[1]].Usage(), Explicit); err != nil {
				t.Fatal("released string-category I/O refused", pair, err)
			}
		}
		rebound, err := p.Rebind(foreign[Text].Usage(), foreign[Polygon].Usage(), Explicit)
		if err != nil {
			t.Fatal(err)
		}
		got, err = rebound.Apply(ctx, String(foreign[Text], "((2,3))"), InputEnvironment{}, OutputEnvironment{})
		if err != nil || got.Type() != foreign[Polygon] {
			t.Fatal("polygon input generation rebind", err)
		}
	})
}
