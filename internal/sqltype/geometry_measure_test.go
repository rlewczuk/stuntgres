package sqltype

import (
	"context"
	"errors"
	"math"
	"testing"
)

func measureUnitPlan(t *testing.T, types map[ID]*Descriptor, op GeometryMeasure) GeometryMeasurePlan {
	t.Helper()
	var inputs [2]Usage
	for i := range op.Arity() {
		inputs[i] = types[op.Formal(i)].Usage()
	}
	p, err := BindGeometryMeasure(op, types[op.ResultID()].Usage(), inputs)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestGeometryMeasureArithmeticOrder(t *testing.T) {
	types, ctx := testTypes(), context.Background()
	// Each witness distinguishes the released checked evaluation from an
	// algebraically equivalent rewrite that would hide the intermediate error.
	for _, tc := range []struct {
		op             GeometryMeasure
		input, outcome string
	}{
		{CenterBox, "(1e308,0),(1e308,0)", "overflow"},
		{CenterBox, "(5e-324,0),(0,0)", "underflow"},
		{AreaPath, "((1e308,1e308))", "overflow"},
		{AreaPath, "((5e-324,0),(0,1),(0,0))", "underflow"},
		{AreaCircle, "<(0,0),1e-162>", "underflow"},
	} {
		t.Run(tc.op.Name()+tc.input, func(t *testing.T) {
			v := circleUnitInput(t, types[tc.op.Formal(0)], tc.input)
			got, err := measureUnitPlan(t, types, tc.op).Evaluate(ctx, [2]Value{v})
			circleUnitError(t, err, "22003", "value out of range: "+tc.outcome)
			if got != (Value{}) {
				t.Fatal("partial result published", got)
			}
		})
	}
	open := circleUnitInput(t, types[Path], "[(1e308,1e308)]")
	got, err := measureUnitPlan(t, types, AreaPath).Evaluate(ctx, [2]Value{open})
	if err != nil || !got.IsNull() || got.Type() != types[Float8] {
		t.Fatal("open path arithmetic was reached", got, err)
	}
	// Raw inverted BOX corners must be consumed, not normalized as fresh input.
	raw := Value{typ: types[Box], geometry: &geometryPayload{coordinates: [4]float64{-2, 4, 2, 0}}}
	got, err = measureUnitPlan(t, types, AreaBox).Evaluate(ctx, [2]Value{raw})
	if err != nil || got.floating != -16 {
		t.Fatal("raw box normalized", got, err)
	}
}

func TestGeometryMeasureAuthority(t *testing.T) {
	types, foreign := testTypes(), testTypes()
	for op := AreaBox; op <= DiameterCircle; op++ {
		p := measureUnitPlan(t, types, op)
		if _, err := BindGeometryMeasure(op, foreign[op.ResultID()].Usage(), [2]Usage{types[op.Formal(0)].Usage()}); err == nil {
			t.Fatal("foreign result admitted", op)
		}
		if _, err := BindGeometryMeasure(op, types[op.ResultID()].Usage(), [2]Usage{foreign[op.Formal(0)].Usage()}); err == nil {
			t.Fatal("foreign input admitted", op)
		}
		if _, err := BindGeometryMeasure(op, types[Text].Usage(), [2]Usage{types[op.Formal(0)].Usage()}); err == nil {
			t.Fatal("wrong result admitted", op)
		}
		if _, err := p.Evaluate(context.Background(), [2]Value{Null(foreign[op.Formal(0)])}); err == nil {
			t.Fatal("foreign NULL retagged", op)
		}
		got, err := p.Evaluate(context.Background(), [2]Value{Null(types[op.Formal(0)])})
		if err != nil || !got.IsNull() || got.Type() != types[op.ResultID()] {
			t.Fatal("strict result authority", op, got, err)
		}
	}
}

type measureCancelContext struct {
	context.Context
	remaining int
}

func (c *measureCancelContext) Err() error {
	if c.remaining == 0 {
		return context.Canceled
	}
	c.remaining--
	return nil
}

func TestGeometryMeasureCancellation(t *testing.T) {
	types := testTypes()
	points := make([][2]float64, 128)
	for i := range points {
		points[i] = [2]float64{float64(i), 1}
	}
	v := Value{typ: types[Path], path: &pathPayload{closed: true, points: points}}
	ctx := &measureCancelContext{Context: context.Background(), remaining: 4}
	got, err := measureUnitPlan(t, types, AreaPath).Evaluate(ctx, [2]Value{v})
	if !errors.Is(err, context.Canceled) || got != (Value{}) {
		t.Fatal("traversal cancellation or partial result", got, err)
	}
	if ctx.remaining != 0 {
		t.Fatal("did not traverse to cancellation")
	}
	// The cancelled computation must not mutate the published input payload.
	for i, point := range v.path.points {
		if point != ([2]float64{float64(i), 1}) {
			t.Fatal("input changed", i, point)
		}
	}
}

func TestGeometryMeasureCenterBits(t *testing.T) {
	types, ctx := testTypes(), context.Background()
	for _, bits := range [][2]uint64{
		{0x8000000000000000, 0},
		{0, 0x8000000000000000},
		{0xfff8000000000001, 0x7ff8000000001234},
		{0x3fb999999999999b, 1},
	} {
		v := Value{typ: types[Circle], circle: circleDatum{x: math.Float64frombits(bits[0]), y: math.Float64frombits(bits[1]), radius: -2}}
		original := v
		got, err := measureUnitPlan(t, types, CenterCircle).Evaluate(ctx, [2]Value{v})
		if err != nil {
			t.Fatal(err)
		}
		x, y := pointCoordinates(got)
		if math.Float64bits(x) != bits[0] || math.Float64bits(y) != bits[1] || got.Type() != types[Point] {
			t.Fatal("center bits/authority lost", bits, got)
		}
		if !SameDatum(v, original) {
			t.Fatal("center mutated input")
		}
	}
}

func TestGeometryMeasureBoxLineKernel(t *testing.T) {
	types, ctx := testTypes(), context.Background()
	for _, tc := range []struct {
		op    GeometryMeasure
		input string
		want  float64
	}{
		{WidthBox, "(4,6),(1,2)", 3},
		{HeightBox, "(4,6),(1,2)", 4},
		{WidthBox, "(1,2),(1,2)", 0},
		{HeightBox, "(1,2),(1,2)", 0},
		{WidthBox, "(4,1e308),(1,-1e308)", 3},
		{HeightBox, "(1e308,6),(-1e308,2)", 4},
	} {
		v := circleUnitInput(t, types[Box], tc.input)
		got, err := measureUnitPlan(t, types, tc.op).Evaluate(ctx, [2]Value{v})
		if err != nil || got.Type() != types[Float8] || got.floating != tc.want {
			t.Fatal(tc, got, err)
		}
	}
	raw := Value{typ: types[Box], geometry: &geometryPayload{coordinates: [4]float64{0, 0, 1, 2}}}
	for _, tc := range []struct {
		op   GeometryMeasure
		want float64
	}{{WidthBox, -1}, {HeightBox, -2}} {
		got, err := measureUnitPlan(t, types, tc.op).Evaluate(ctx, [2]Value{raw})
		if err != nil || got.floating != tc.want {
			t.Fatal("raw corners normalized", got, err)
		}
	}
	for _, coordinates := range [][4]float64{
		{4, 6, 1, 2}, {1, 2, 1, 2}, {0, 0, 1, 2},
		{1e308, 6, -1e308, 2},
		{math.Copysign(0, -1), 0, math.Float64frombits(0xfff8000000000001), math.SmallestNonzeroFloat64},
	} {
		a := Value{typ: types[Box], geometry: &geometryPayload{coordinates: coordinates}}
		got, err := measureUnitPlan(t, types, DiagonalBox).Evaluate(ctx, [2]Value{a})
		if err != nil || got.Type() != types[Lseg] || got.geometry != a.geometry {
			t.Fatal("diagonal payload/authority", got, err)
		}
		for i, v := range coordinates {
			if math.Float64bits(got.geometry.coordinates[i]) != math.Float64bits(v) {
				t.Fatal("diagonal changed coordinate", i)
			}
		}
	}
	for _, tc := range []struct {
		a, b string
		want float64
	}{
		{"(0,0)", "(2,1)", .5}, {"(2,1)", "(0,0)", .5},
		{"(0,1)", "(2,0)", -.5}, {"(1,0)", "(1,2)", math.Inf(1)},
		{"(0,2)", "(3,2)", 0}, {"(1,2)", "(1,2)", math.Inf(1)},
		{"(0,0)", "(.0000005,2)", math.Inf(1)},
		{"(0,0)", "(2,.0000005)", 0}, {"(0,0)", "(.0000005,0)", math.Inf(1)},
	} {
		a, b := circleUnitInput(t, types[Point], tc.a), circleUnitInput(t, types[Point], tc.b)
		got, err := measureUnitPlan(t, types, SlopePoints).Evaluate(ctx, [2]Value{a, b})
		if err != nil || got.Type() != types[Float8] || math.Float64bits(got.floating) != math.Float64bits(tc.want) {
			t.Fatal(tc, got, err)
		}
	}
}

func TestGeometryMeasureBoxLineAuthority(t *testing.T) {
	types, foreign := testTypes(), testTypes()
	for op := WidthBox; op <= SlopePoints; op++ {
		var inputs [2]Usage
		var values [2]Value
		for i := range op.Arity() {
			inputs[i] = types[op.Formal(i)].Usage()
			values[i] = Null(types[op.Formal(i)])
		}
		p := measureUnitPlan(t, types, op)
		for i := range op.Arity() {
			bad := inputs
			bad[i] = foreign[op.Formal(i)].Usage()
			if _, err := BindGeometryMeasure(op, types[op.ResultID()].Usage(), bad); err == nil {
				t.Fatal("foreign formal admitted", op, i)
			}
			bad = inputs
			bad[i].Typmod = 1
			if _, err := BindGeometryMeasure(op, types[op.ResultID()].Usage(), bad); err == nil {
				t.Fatal("invalid usage admitted", op, i)
			}
			badValues := values
			badValues[i] = Null(foreign[op.Formal(i)])
			if _, err := p.Evaluate(context.Background(), badValues); err == nil {
				t.Fatal("NULL hid foreign formal", op, i)
			}
			badValues[i] = Null(types[Text])
			if _, err := p.Evaluate(context.Background(), badValues); err == nil {
				t.Fatal("NULL hid wrong formal", op, i)
			}
		}
		if _, err := BindGeometryMeasure(op, foreign[op.ResultID()].Usage(), inputs); err == nil {
			t.Fatal("foreign result admitted", op)
		}
		got, err := p.Evaluate(context.Background(), values)
		if err != nil || !got.IsNull() || got.Type() != types[op.ResultID()] {
			t.Fatal("typed NULL authority", op, got, err)
		}
	}
}

func TestGeometryMeasureBoxLineCancellation(t *testing.T) {
	types := testTypes()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for op := WidthBox; op <= SlopePoints; op++ {
		got, err := measureUnitPlan(t, types, op).Evaluate(ctx, [2]Value{})
		if !errors.Is(err, context.Canceled) || got != (Value{}) {
			t.Fatal("cancellation published result", op, got, err)
		}
	}
}

func TestGeometryMeasurePathAuthority(t *testing.T) {
	types, foreign := testTypes(), testTypes()
	for op := IsClosedPath; op <= NPointsPolygon; op++ {
		inputs := [2]Usage{types[op.Formal(0)].Usage()}
		plan := measureUnitPlan(t, types, op)
		if _, err := BindGeometryMeasure(op, foreign[op.ResultID()].Usage(), inputs); err == nil {
			t.Fatal("foreign result admitted", op)
		}
		if _, err := BindGeometryMeasure(op, types[op.ResultID()].Usage(), [2]Usage{foreign[op.Formal(0)].Usage()}); err == nil {
			t.Fatal("foreign formal admitted", op)
		}
		if _, err := plan.Evaluate(context.Background(), [2]Value{Null(foreign[op.Formal(0)])}); err == nil {
			t.Fatal("foreign NULL admitted", op)
		}
		got, err := plan.Evaluate(context.Background(), [2]Value{Null(types[op.Formal(0)])})
		if err != nil || !got.IsNull() || got.Type() != types[op.ResultID()] {
			t.Fatal("strict typed NULL", op, got, err)
		}
		got, err = plan.Evaluate(context.Background(), [2]Value{{typ: types[op.Formal(0)]}})
		if err == nil || got != (Value{}) {
			t.Fatal("missing payload silently accepted", op, got, err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		got, err = plan.Evaluate(ctx, [2]Value{})
		if !errors.Is(err, context.Canceled) || got != (Value{}) {
			t.Fatal("canceled evaluation published", op, got, err)
		}
	}
}

func TestGeometryMeasurePathClosureBits(t *testing.T) {
	types := testTypes()
	points := [][2]float64{
		{math.Copysign(0, -1), math.SmallestNonzeroFloat64},
		{math.Float64frombits(0xfff8000000000001), math.Float64frombits(0x7ff8000000001234)},
		{math.Copysign(0, -1), math.SmallestNonzeroFloat64},
	}
	wantBits := [][2]uint64{
		{0x8000000000000000, 1},
		{0xfff8000000000001, 0x7ff8000000001234},
		{0x8000000000000000, 1},
	}
	for _, closed := range []bool{false, true} {
		source := Value{typ: types[Path], path: &pathPayload{closed: closed, points: points}}
		for _, op := range []GeometryMeasure{ClosePath, OpenPath} {
			plan := measureUnitPlan(t, types, op)
			got, err := plan.Evaluate(context.Background(), [2]Value{source})
			wantClosed := op == ClosePath
			if err != nil || got.Type() != types[Path] || got.path.closed != wantClosed || source.path.closed != closed {
				t.Fatal("closure/source authority", op, got, err)
			}
			if len(got.path.points) != len(points) || &got.path.points[0] != &source.path.points[0] {
				t.Fatal("closure copied immutable points")
			}
			if (got.path == source.path) != (closed == wantClosed) {
				t.Fatal("idempotent conversion did not reuse original header")
			}
			for i, point := range got.path.points {
				for axis := range point {
					if math.Float64bits(point[axis]) != wantBits[i][axis] {
						t.Fatal("coordinate bits changed", i, axis)
					}
				}
			}
			again, err := plan.Evaluate(context.Background(), [2]Value{got})
			if err != nil || again.path != got.path {
				t.Fatal("repeated conversion allocated header", err)
			}
		}
	}
}

func TestGeometryMeasurePathLengthKernel(t *testing.T) {
	types := testTypes()
	for _, tc := range []struct {
		op    GeometryMeasure
		input string
		want  float64
	}{
		{LengthPath, "[(0,0),(4,0),(0,3)]", 9},
		{LengthPath, "((0,0),(4,0),(0,3))", 12},
		{LengthPath, "[(NaN,Infinity)]", 0},
		{LengthPath, "((NaN,Infinity))", math.NaN()},
		{LengthLseg, "[(3,4),(0,0)]", 5},
		{LengthLseg, "[(0,0),(Infinity,NaN)]", math.Inf(1)},
		{LengthLseg, "[(0,0),(5e-324,0)]", math.SmallestNonzeroFloat64},
	} {
		v := circleUnitInput(t, types[tc.op.Formal(0)], tc.input)
		got, err := measureUnitPlan(t, types, tc.op).Evaluate(context.Background(), [2]Value{v})
		if err != nil || got.Type() != types[Float8] || !(got.floating == tc.want || math.IsNaN(got.floating) && math.IsNaN(tc.want)) {
			t.Fatal(tc, got, err)
		}
	}
	for _, tc := range []struct {
		op    GeometryMeasure
		input string
	}{
		{LengthLseg, "[(-1e308,0),(1e308,0)]"},
		{LengthLseg, "[(0,0),(1.3e308,1.3e308)]"},
		{LengthPath, "[(0,0),(1e308,0),(0,0)]"},
	} {
		v := circleUnitInput(t, types[tc.op.Formal(0)], tc.input)
		got, err := measureUnitPlan(t, types, tc.op).Evaluate(context.Background(), [2]Value{v})
		circleUnitError(t, err, "22003", "value out of range: overflow")
		if got != (Value{}) {
			t.Fatal("arithmetic failure published partial length")
		}
	}
}

func TestGeometryMeasurePathLengthOrderCancellation(t *testing.T) {
	types := testTypes()
	plan := measureUnitPlan(t, types, LengthPath)
	points := [][2]float64{{-1e308, 0}, {0, 0}, {1e308, 0}}
	// Closed traversal must evaluate last->first before the cancelable adjacent
	// loop. Open traversal instead reaches cancellation before any segment.
	for _, closed := range []bool{false, true} {
		v := Value{typ: types[Path], path: &pathPayload{closed: closed, points: points}}
		ctx := &measureCancelContext{Context: context.Background(), remaining: 1}
		got, err := plan.Evaluate(ctx, [2]Value{v})
		if closed {
			circleUnitError(t, err, "22003", "value out of range: overflow")
		} else if !errors.Is(err, context.Canceled) {
			t.Fatal("open loop did not observe cancellation", err)
		}
		if got != (Value{}) || v.path.closed != closed || v.path.points[0][0] != -1e308 {
			t.Fatal("failure published or mutated source")
		}
	}
	v := circleUnitInput(t, types[Path], "[(0,0),(1,0),(2,0),(3,0),(4,0)]")
	ctx := &measureCancelContext{Context: context.Background(), remaining: 3}
	got, err := plan.Evaluate(ctx, [2]Value{v})
	if !errors.Is(err, context.Canceled) || got != (Value{}) || ctx.remaining != 0 {
		t.Fatal("mid-loop cancellation/partial publication", got, err)
	}
}
