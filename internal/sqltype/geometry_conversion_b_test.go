package sqltype

import (
	"context"
	"math"
	"testing"
)

func TestGeometryConversionBEdges(t *testing.T) {
	types, ctx := testTypes(), context.Background()
	evaluate := func(op GeometryConversion, values ...Value) Value {
		t.Helper()
		var args [2]Value
		copy(args[:], values)
		got, err := conversionUnitPlan(t, types, op).Evaluate(ctx, args)
		if err != nil {
			t.Fatal(err)
		}
		if got.Type() != types[op.ResultID()] {
			t.Fatal("result descriptor")
		}
		return got
	}
	bits := [2]float64{math.Copysign(0, -1), math.Float64frombits(0xfff8000000000001)}
	assertPoint := func(v Value, x, y float64) {
		t.Helper()
		a, b := pointCoordinates(v)
		if math.Float64bits(a) != math.Float64bits(x) || math.Float64bits(b) != math.Float64bits(y) {
			t.Fatal("point coordinate bits changed")
		}
	}
	point := pointValue(types[Point], bits[0], bits[1])
	x := Value{typ: types[Float8], floating: bits[0]}
	y := Value{typ: types[Float8], floating: bits[1]}
	assertPoint(evaluate(PointCoordinates, x, y), bits[0], bits[1])
	for _, radius := range []float64{-1, math.NaN(), math.Inf(1)} {
		circle := Value{typ: types[Circle], circle: circleDatum{x: bits[0], y: bits[1], radius: radius}}
		assertPoint(evaluate(PointCircle, circle), bits[0], bits[1])
	}
	segment := evaluate(LsegPoints, point, point)
	for i, n := range segment.geometry.coordinates {
		if math.Float64bits(n) != math.Float64bits(bits[i%2]) {
			t.Fatal("segment bits changed")
		}
	}
	raw := Value{typ: types[Box], geometry: &geometryPayload{coordinates: [4]float64{-2, -3, 4, 5}}}
	diagonal := evaluate(LsegBox, raw)
	if diagonal.geometry != raw.geometry || diagonal.geometry.coordinates != raw.geometry.coordinates {
		t.Fatal("raw diagonal normalized or copied")
	}
	for _, text := range []string{"((2,3))", "((0,0),(1,1))", "((0,0),(0,0),(6,0))", "((0,0),(1,1),(0,0))", "((-0,-NaN),(5e-324,Infinity))"} {
		polygon := circleUnitInput(t, types[Polygon], text)
		path := evaluate(PathPolygon, polygon)
		if !path.path.closed || len(path.path.points) != len(polygon.polygon.points) {
			t.Fatal("closure or vertex count")
		}
		for i, p := range polygon.polygon.points {
			if math.Float64bits(path.path.points[i][0]) != math.Float64bits(p.x) || math.Float64bits(path.path.points[i][1]) != math.Float64bits(p.y) {
				t.Fatal("path vertex bits/order")
			}
		}
		original := circleUnitInput(t, types[Polygon], text)
		if !SameDatum(polygon, original) {
			t.Fatal("polygon mutated")
		}
	}
	for _, op := range []GeometryConversion{PointBox, PointLseg} {
		for _, tc := range []struct {
			coordinates [4]float64
			x, y        float64
		}{
			{[4]float64{2, 4, 8, 10}, 5, 7},
			{[4]float64{8, 10, 2, 4}, 5, 7},
			{[4]float64{math.Copysign(0, -1), 0, math.Copysign(0, -1), 0}, math.Copysign(0, -1), 0},
		} {
			v := Value{typ: types[op.Formal(0)], geometry: &geometryPayload{coordinates: tc.coordinates}}
			assertPoint(evaluate(op, v), tc.x, tc.y)
		}
	}
	for _, tc := range []struct {
		op            GeometryConversion
		text, message string
	}{
		{PointBox, "(1e308,0),(1e308,0)", "overflow"},
		{PointLseg, "[(1e308,0),(1e308,0)]", "overflow"},
		{PointBox, "(5e-324,0),(0,0)", "underflow"},
		{PointLseg, "[(5e-324,0),(0,0)]", "underflow"},
		{PointPolygon, "((1e308,0),(1e308,0))", "overflow"},
		{PointPolygon, "((0,0),(5e-324,0))", "underflow"},
		{PointPolygon, "((1e308,0),(-1e308,0))", "overflow"},
		{CirclePolygon, "((1e308,0),(-1e308,0))", "overflow"},
	} {
		value := circleUnitInput(t, types[tc.op.Formal(0)], tc.text)
		_, err := conversionUnitPlan(t, types, tc.op).Evaluate(ctx, [2]Value{value})
		circleUnitError(t, err, "22003", "value out of range: "+tc.message)
	}
}
