package sqltype

import (
	"context"
	"errors"
	"math"
	"testing"
)

func polygonUnitInput(t *testing.T, d *Descriptor, text string) Value {
	t.Helper()
	v, err := d.Input(context.Background(), text, -1)
	if err != nil {
		t.Fatal(text, err)
	}
	return v
}
func TestPolygonInputOutput(t *testing.T) {
	ctx := context.Background()
	types := testTypes()
	d := types[Polygon]
	for _, tc := range []struct{ input, output string }{
		{"((0,0),(4,0),(0,4))", "((0,0),(4,0),(0,4))"},
		{"(0,0),(4,0),(0,4)", "((0,0),(4,0),(0,4))"},
		{"(0,0,4,0,0,4)", "((0,0),(4,0),(0,4))"},
		{"0,0,4,0,0,4", "((0,0),(4,0),(0,4))"},
		{"0,0", "((0,0))"}, {"0,0,1,1", "((0,0),(1,1))"},
		{"((4,0),(0,4),(0,0),(4,0))", "((4,0),(0,4),(0,0),(4,0))"},
		{" ( ( 1 , 2 ) , ( 3 , 4 ) ) ", "((1,2),(3,4))"},
		{"0x1p0,+NaN", "((1,NaN))"}, {"NaN(payload),-Infinity", "((NaN,-Infinity))"},
		{"0.10000000000000002,5e-324,-0,+Infinity", "((0.10000000000000002,5e-324),(-0,Infinity))"},
	} {
		v := polygonUnitInput(t, d, tc.input)
		out, err := d.Output(ctx, v)
		if err != nil || out != tc.output {
			t.Fatal(tc, out, err)
		}
		literal, err := d.SQLLiteral(ctx, v)
		if err != nil || literal != "'"+tc.output+"'" {
			t.Fatal(literal, err)
		}
		if !SameDatum(v, polygonUnitInput(t, d, literal[1:len(literal)-1])) {
			t.Fatal("lossy literal", tc)
		}
	}
	for _, tc := range []struct{ name, input, output string }{
		{"negativeNaNX", "-NaN,0", "((NaN,0))"},
		{"negativeNaNY", "0,-NaN", "((0,NaN))"},
		{"negativeNaNBoth", "-NaN,-NaN", "((NaN,NaN))"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := polygonUnitInput(t, d, tc.input)
			out, err := d.Output(ctx, v)
			if err != nil || out != tc.output {
				t.Fatal("canonical display", out, err)
			}
			literal, err := d.SQLLiteral(ctx, v)
			if err != nil {
				t.Fatal(err)
			}
			if !SameDatum(v, polygonUnitInput(t, d, literal[1:len(literal)-1])) {
				t.Fatal("lossy negative NaN literal", tc.input, literal)
			}
		})
	}
	v := polygonUnitInput(t, d, "0.10000000000000002,5e-324")
	out, err := d.OutputWithEnvironment(ctx, v, OutputEnvironment{FloatPrecisionSet: true, ExtraFloatDigits: 0})
	if err != nil || out != "((0.1,4.94065645841247e-324))" {
		t.Fatal(out, err)
	}
	for _, text := range []string{"", "()", "[(0,0),(1,1)]", "0,0,1", "bad,0", "((0,0),(1,1)", "0,0 junk", "1e999,0,1", " ( ( 1 , 2 ) ( 3 , 4 ) ) "} {
		_, err := d.Input(ctx, text, -1)
		circleUnitError(t, err, "22P02", "invalid input syntax for type polygon: \""+text+"\"")
	}
	for _, text := range []string{"1e999", "1e-999"} {
		_, err := d.Input(ctx, text+",0", -1)
		circleUnitError(t, err, "22003", "\""+text+"\" is out of range for type double precision")
	}
	if !d.Concrete() || d == types[602] || types[1027].Concrete() || !types[1027].IsArray() || types[1027].Element() != d || d.AssociatedArray() != 1027 {
		t.Fatal("polygon/path/array identity boundary")
	}
	if equal, order := ComparisonCapabilities(d); equal || order {
		t.Fatal("polygon has no default comparison")
	}
	m, ok := d.PGTypeMetadata()
	if !ok || m.Length != -1 || m.Alignment != "d" || m.Storage != "x" || m.Element != 0 || m.Subscript != 0 || m.Input != 347 || m.Output != 348 || m.Receive != 2486 || m.Send != 2487 {
		t.Fatal(m, ok)
	}
	for _, invalid := range []Value{{typ: d}, {typ: d, polygon: &polygonPayload{}}} {
		if _, err := d.Output(ctx, invalid); err == nil {
			t.Fatal("missing output payload")
		}
		if _, err := d.SQLLiteral(ctx, invalid); err == nil {
			t.Fatal("missing literal payload")
		}
		if _, err := invalid.Retag(d); err == nil {
			t.Fatal("missing retag payload")
		}
	}
	if _, err := Null(d).Retag(d); err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := d.Input(canceled, "0,0", -1); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := d.Output(canceled, v); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
func TestPolygonDatumIdentity(t *testing.T) {
	d := testTypes()[Polygon]
	v := polygonUnitInput(t, d, "0,0,1,0,0,1")
	copy := v
	if copy.polygon != v.polygon || !SameDatum(v, copy) {
		t.Fatal("value copy must share immutable payload")
	}
	independent := polygonUnitInput(t, d, "0,0,1,0,0,1")
	if independent.polygon == v.polygon || !SameDatum(v, independent) {
		t.Fatal("independent input ownership")
	}
	for _, text := range []string{"-0,0,1,0,0,1", "0,0,1,0,0,1.0000001", "1,0,0,1,0,0", "0,0,1,0,0,1,0,0"} {
		if SameDatum(v, polygonUnitInput(t, d, text)) {
			t.Fatal("collapsed datum bits/order/count", text)
		}
	}
	bits := []uint64{0x3fb999999999999b, 1, 1 << 63, 0x7ff0000000000000}
	precise := polygonUnitInput(t, d, "0.10000000000000002,5e-324,-0,Infinity")
	for i, p := range precise.polygon.points {
		if math.Float64bits(p.x) != bits[2*i] || math.Float64bits(p.y) != bits[2*i+1] {
			t.Fatal("coordinate bits", i, p)
		}
	}
	a := Value{typ: d, polygon: &polygonPayload{points: []polygonPoint{{math.Float64frombits(0x7ff8000000000001), 0}}}}
	b := Value{typ: d, polygon: &polygonPayload{points: []polygonPoint{{math.Float64frombits(0x7ff8000000000002), 0}}}}
	if !SameDatum(a, a) || SameDatum(a, b) {
		t.Fatal("NaN bit identity")
	}
	if SameDatum(v, polygonUnitInput(t, testTypes()[Polygon], "0,0,1,0,0,1")) {
		t.Fatal("foreign descriptor identity")
	}
}
func TestPolygonOperators(t *testing.T) {
	ctx := context.Background()
	types := testTypes()
	for _, tc := range []struct {
		op   PolygonOperator
		a, b string
		want bool
	}{
		{PolygonSame, "0,0,4,0,0,4", "4,0,0,4,0,0", true},
		{PolygonSame, "0,0,4,0,0,4", "1,1,5,1,1,5", false},
		{PolygonSame, "0,0", "0.0000005,0", true},
		{PolygonSame, "NaN,0", "NaN,0.0000005", false},
		{PolygonSame, "0,0,0,0,1,0,0,1", "0,0,1,0,0,1,0,0", true},
		{PolygonContainsPoint, "0,0,4,0,0,4", "(1,1)", true},
		{PolygonContainsPoint, "0,0,4,0,0,4", "(2,2)", true},
		{PolygonContainsPoint, "0,0,4,0,0,4", "(3,3)", false},
		{PointContainedPolygon, "(1,1)", "0,0,4,0,0,4", true},
		{PolygonContainsPoint, "0,0", "(0,0)", true},
		{PolygonContainsPoint, "0,0", "(1,0)", false},
		{PolygonContainsPoint, "0,0,4,0", "(2,0)", true},
		{PolygonContainsPoint, "0,0,4,4,0,4,4,0", "(2,2)", true},
		// Released NaN branches: no fuzzy zero/sign match, final crossing
		// contributes -2. Infinity produces NaN products with the same result.
		{PolygonContainsPoint, "NaN,NaN", "(0,0)", true},
		{PolygonContainsPoint, "Infinity,Infinity", "(0,0)", false},
	} {
		left, right := tc.op.Signature()
		p, err := BindPolygon(tc.op, types[Bool].Usage(), types[left].Usage(), types[right].Usage())
		if err != nil {
			t.Fatal(err)
		}
		args := [2]Value{polygonUnitInput(t, types[left], tc.a), polygonUnitInput(t, types[right], tc.b)}
		v, err := p.Evaluate(ctx, args)
		if err != nil || v.typ != types[Bool] || v.null || v.boolean != tc.want {
			t.Fatal(tc, v, err)
		}
		for i := range args {
			nulls := args
			nulls[i] = Null(args[i].typ)
			v, err := p.Evaluate(ctx, nulls)
			if err != nil || !v.null || v.typ != types[Bool] {
				t.Fatal(v, err)
			}
		}
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		if _, err := p.Evaluate(canceled, args); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		foreign := testTypes()
		if _, err := BindPolygon(tc.op, foreign[Bool].Usage(), types[left].Usage(), types[right].Usage()); err == nil {
			t.Fatal("foreign result authority")
		}
	}
	p, _ := BindPolygon(PolygonContainsPoint, types[Bool].Usage(), types[Polygon].Usage(), types[Point].Usage())
	_, err := p.Evaluate(ctx, [2]Value{polygonUnitInput(t, types[Polygon], "1e308,1,-1e308,-1,0,2"), polygonUnitInput(t, types[Point], "(-1e308,0)")})
	circleUnitError(t, err, "22003", "value out of range: overflow")
	_, err = p.Evaluate(ctx, [2]Value{{typ: types[Polygon]}, Null(types[Point])})
	if err == nil {
		t.Fatal("missing non-NULL polygon payload")
	}
	// Independently exercise the actual checked arithmetic used by crossing;
	// epsilon branches make a tiny-coordinate public underflow claim invalid.
	_, err = checkedFloatingBinary(ArithmeticMultiply, Float8, 1e-200, 1e-200)
	circleUnitError(t, err, "22003", "value out of range: underflow")
	_, err = checkedFloatingBinary(ArithmeticSubtract, Float8, 1e308, -1e308)
	circleUnitError(t, err, "22003", "value out of range: overflow")
}
