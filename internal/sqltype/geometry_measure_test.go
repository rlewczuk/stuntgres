package sqltype

import (
	"context"
	"errors"
	"math"
	"testing"
)

func measureUnitPlan(t *testing.T, types map[ID]*Descriptor, op GeometryMeasure) GeometryMeasurePlan {
	t.Helper()
	p, err := BindGeometryMeasure(op, types[op.ResultID()].Usage(), types[op.Formal()].Usage())
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
			v := circleUnitInput(t, types[tc.op.Formal()], tc.input)
			got, err := measureUnitPlan(t, types, tc.op).Evaluate(ctx, v)
			circleUnitError(t, err, "22003", "value out of range: "+tc.outcome)
			if got != (Value{}) {
				t.Fatal("partial result published", got)
			}
		})
	}
	open := circleUnitInput(t, types[Path], "[(1e308,1e308)]")
	got, err := measureUnitPlan(t, types, AreaPath).Evaluate(ctx, open)
	if err != nil || !got.IsNull() || got.Type() != types[Float8] {
		t.Fatal("open path arithmetic was reached", got, err)
	}
	// Raw inverted BOX corners must be consumed, not normalized as fresh input.
	raw := Value{typ: types[Box], geometry: &geometryPayload{coordinates: [4]float64{-2, 4, 2, 0}}}
	got, err = measureUnitPlan(t, types, AreaBox).Evaluate(ctx, raw)
	if err != nil || got.floating != -16 {
		t.Fatal("raw box normalized", got, err)
	}
}

func TestGeometryMeasureAuthority(t *testing.T) {
	types, foreign := testTypes(), testTypes()
	for op := AreaBox; op <= DiameterCircle; op++ {
		p := measureUnitPlan(t, types, op)
		if _, err := BindGeometryMeasure(op, foreign[op.ResultID()].Usage(), types[op.Formal()].Usage()); err == nil {
			t.Fatal("foreign result admitted", op)
		}
		if _, err := BindGeometryMeasure(op, types[op.ResultID()].Usage(), foreign[op.Formal()].Usage()); err == nil {
			t.Fatal("foreign input admitted", op)
		}
		if _, err := BindGeometryMeasure(op, types[Text].Usage(), types[op.Formal()].Usage()); err == nil {
			t.Fatal("wrong result admitted", op)
		}
		if _, err := p.Evaluate(context.Background(), Null(foreign[op.Formal()])); err == nil {
			t.Fatal("foreign NULL retagged", op)
		}
		got, err := p.Evaluate(context.Background(), Null(types[op.Formal()]))
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
	got, err := measureUnitPlan(t, types, AreaPath).Evaluate(ctx, v)
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
		got, err := measureUnitPlan(t, types, CenterCircle).Evaluate(ctx, v)
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
