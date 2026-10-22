package sqltype

import (
	"context"
	"errors"
	"math"
	"testing"
)

func conversionUnitPlan(t *testing.T, types map[ID]*Descriptor, op GeometryConversion) GeometryConversionPlan {
	t.Helper()
	var usages [2]Usage
	for i := range op.Arity() {
		usages[i] = types[op.Formal(i)].Usage()
	}
	p, err := BindGeometryConversion(op, types[op.ResultID()].Usage(), usages)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestGeometryConversionKernel(t *testing.T) {
	types, ctx := testTypes(), context.Background()
	for _, tc := range []struct {
		op     GeometryConversion
		inputs [2]string
		want   string
	}{
		{BoxCircle, [2]string{"<(0,0),2>"}, "(1.414213562373095,1.414213562373095),(-1.414213562373095,-1.414213562373095)"},
		{BoxPoint, [2]string{"(1,0)"}, "(1,0),(1,0)"},
		{BoxPoints, [2]string{"(0,1)", "(1,0)"}, "(1,1),(0,0)"},
		{BoxPolygon, [2]string{"((0,0),(1,1),(2,0))"}, "(2,1),(0,0)"},
		{BoundBox, [2]string{"(1,1),(0,0)", "(4,4),(3,3)"}, "(4,4),(0,0)"},
		{CircleBox, [2]string{"(6,8),(0,0)"}, "<(3,4),5>"},
		{CirclePoint, [2]string{"(0,0)", "-1"}, "<(0,0),-1>"},
		{CirclePolygon, [2]string{"((0,0),(1,3),(2,0))"}, "<(1,1),1.6094757082487299>"},
		{LinePoints, [2]string{"(-1,0)", "(1,0)"}, "{0,-1,0}"},
	} {
		t.Run(tc.op.Name()+tc.want, func(t *testing.T) {
			p := conversionUnitPlan(t, types, tc.op)
			var values [2]Value
			for i := range tc.op.Arity() {
				values[i] = circleUnitInput(t, types[tc.op.Formal(i)], tc.inputs[i])
			}
			original := values
			got, err := p.Evaluate(ctx, values)
			if err != nil {
				t.Fatal(err)
			}
			out, err := got.Type().Output(ctx, got)
			if err != nil || out != tc.want {
				t.Fatalf("got %s %v want %s", out, err, tc.want)
			}
			for i := range tc.op.Arity() {
				if !SameDatum(values[i], original[i]) {
					t.Fatal("mutated operand")
				}
				null := values
				null[i] = Null(values[i].Type())
				got, err := p.Evaluate(ctx, null)
				if err != nil || !got.IsNull() || got.Type() != types[tc.op.ResultID()] {
					t.Fatal("strict typed NULL", got, err)
				}
				foreign := values
				foreign[i] = Null(testTypes()[tc.op.Formal(i)])
				if _, err := p.Evaluate(ctx, foreign); err == nil {
					t.Fatal("foreign value accepted")
				}
			}
			foreignTypes := testTypes()
			var usages [2]Usage
			for i := range tc.op.Arity() {
				usages[i] = types[tc.op.Formal(i)].Usage()
			}
			if _, err := BindGeometryConversion(tc.op, foreignTypes[tc.op.ResultID()].Usage(), usages); err == nil {
				t.Fatal("foreign result accepted")
			}
			cancelled, cancel := context.WithCancel(ctx)
			cancel()
			if _, err := p.Evaluate(cancelled, values); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		})
	}
	point := pointValue(types[Point], math.Copysign(0, -1), math.Float64frombits(0xfff8000000000001))
	box, err := conversionUnitPlan(t, types, BoxPoint).Evaluate(ctx, [2]Value{point})
	if err != nil {
		t.Fatal(err)
	}
	for i := range 2 {
		if math.Float64bits(box.geometry.coordinates[i*2]) != math.Float64bits(point.floating) || math.Float64bits(box.geometry.coordinates[i*2+1]) != uint64(point.integer) {
			t.Fatal("point bits lost")
		}
	}
	negative, err := conversionUnitPlan(t, types, CirclePoint).Evaluate(ctx, [2]Value{pointValue(types[Point], 0, 0), circleUnitInput(t, types[Float8], "-1")})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := conversionUnitPlan(t, types, BoxCircle).Evaluate(ctx, [2]Value{negative})
	if err != nil || raw.geometry.coordinates[0] >= raw.geometry.coordinates[2] {
		t.Fatal("negative radius normalized", raw, err)
	}
	_, err = conversionUnitPlan(t, types, CircleBox).Evaluate(ctx, [2]Value{circleUnitInput(t, types[Box], "(1e308,1e308),(1e308,1e308)")})
	circleUnitError(t, err, "22003", "value out of range: overflow")
}

func TestGeometryConversionLineErrors(t *testing.T) {
	types := testTypes()
	for _, endpoints := range [][2]string{{"(1,1)", "(1,1)"}, {"(0,0)", "(0.000001,0.000001)"}, {"(NaN,2)", "(NaN,2)"}} {
		args := [2]Value{circleUnitInput(t, types[Point], endpoints[0]), circleUnitInput(t, types[Point], endpoints[1])}
		_, err := conversionUnitPlan(t, types, LinePoints).Evaluate(context.Background(), args)
		circleUnitError(t, err, "22023", "invalid line specification: must be two distinct points")
		_, err = types[Line].Input(context.Background(), "["+endpoints[0]+","+endpoints[1]+"]", -1)
		circleUnitError(t, err, "22P02", "invalid line specification: must be two distinct points")
	}
}

func TestCircleConstructorReplay(t *testing.T) {
	types, ctx := testTypes(), context.Background()
	center := pointValue(types[Point], math.Copysign(0, -1), math.Float64frombits(0xfff8000000000001))
	radius := circleUnitInput(t, types[Float8], "-1")
	value, err := conversionUnitPlan(t, types, CirclePoint).Evaluate(ctx, [2]Value{center, radius})
	if err != nil {
		t.Fatal(err)
	}
	literal, err := types[Circle].SQLLiteral(ctx, value)
	if err != nil || literal != "pg_catalog.circle('(-0,-NaN)'::pg_catalog.point,'-1'::pg_catalog.float8)" {
		t.Fatal(literal, err)
	}
	if !value.RequiresCircleConstructorReplay() || Null(types[Circle]).RequiresCircleConstructorReplay() {
		t.Fatal("replay predicate")
	}
	_, err = types[Circle].Input(ctx, "<(0,0),-1>", -1)
	circleUnitError(t, err, "22P02", "invalid input syntax for type circle: \"<(0,0),-1>\"")
	for _, op := range []CircleOperator{CircleRadius, CircleSame, CircleEqual, CircleDistance, CircleContains} {
		result := types[op.ResultID()].Usage()
		p, err := BindCircle(op, result, types[Circle].Usage(), types[Circle].Usage())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := p.Evaluate(ctx, [2]Value{value, value}); err != nil {
			t.Fatal("valid negative datum rejected", op, err)
		}
	}
	t.Run("known_text_input", func(t *testing.T) {
		p, err := PlanCoercion(types[Text].Usage(), types[Circle].Usage(), Explicit)
		if err != nil || !p.FunctionCastShortcut() {
			t.Fatal("missing selected explicit input plan", err)
		}
		input := circleUnitInput(t, types[Text], "<(2,3),4>")
		got, err := p.Apply(ctx, input, InputEnvironment{}, OutputEnvironment{})
		if err != nil {
			t.Fatal(err)
		}
		if text, err := types[Circle].Output(ctx, got); err != nil || text != "<(2,3),4>" {
			t.Fatal("ordinary circle input route", text, err)
		}
		got, err = p.Apply(ctx, Null(types[Text]), InputEnvironment{}, OutputEnvironment{})
		if err != nil || !got.IsNull() || got.Type() != types[Circle] {
			t.Fatal("typed input NULL", got, err)
		}
		_, err = p.Apply(ctx, circleUnitInput(t, types[Text], "<(0,0),-1>"), InputEnvironment{}, OutputEnvironment{})
		circleUnitError(t, err, "22P02", "invalid input syntax for type circle: \"<(0,0),-1>\"")
		for _, mode := range []CoercionContext{Implicit, Assignment} {
			if _, err := PlanCoercion(types[Text].Usage(), types[Circle].Usage(), mode); err == nil {
				t.Fatal("selected typename input widened coercion context", mode)
			}
		}
		for _, pair := range [][2]ID{{Circle, Text}, {VarChar, Circle}, {Point, Circle}, {Float8, Circle}} {
			if _, err := PlanCoercion(types[pair[0]].Usage(), types[pair[1]].Usage(), Explicit); err == nil {
				t.Fatal("selected input widened adjacent edge", pair)
			}
		}
		fresh := testTypes()
		rebound, err := p.Rebind(fresh[Text].Usage(), fresh[Circle].Usage(), Explicit)
		if err != nil {
			t.Fatal(err)
		}
		got, err = rebound.Apply(ctx, circleUnitInput(t, fresh[Text], "<(2,3),4>"), InputEnvironment{}, OutputEnvironment{})
		if err != nil || got.Type() != fresh[Circle] {
			t.Fatal("input plan rebind ownership", got, err)
		}
	})
}
