package sqltype

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"

	"stuntgres/internal/sqlstate"
)

func lsegUnitError(t *testing.T, err error, code, message string) {
	t.Helper()
	var d *sqlstate.Error
	if !errors.As(err, &d) || d.Code != code || d.Message != message {
		t.Fatalf("diagnostic %v want %s / %s", err, code, message)
	}
}

func TestLsegInputOutput(t *testing.T) {
	d, ctx := testTypes()[Lseg], context.Background()
	for _, input := range []string{"[(1,4),(3,2)]", "((1,4),(3,2))", "(1,4),(3,2)", "1,4,3,2", "(1,4,3,2)", "(1,4)(3,2)", "1,4,3,2,", "[(1,4),(3,2))", "1,4,3,2 ", "[(1,4),(3,2),]", "[(1,4),(3,2) ,]", "[(1,4),(3,2)] "} {
		v := boxUnitInput(t, d, input)
		if out, err := d.Output(ctx, v); err != nil || out != "[(1,4),(3,2)]" {
			t.Fatal(input, out, err)
		}
		if v.geometry.coordinates != [4]float64{1, 4, 3, 2} {
			t.Fatal("endpoint order changed", v.geometry.coordinates)
		}
	}
	for _, tc := range [][2]string{{"3,2,1,4", "[(3,2),(1,4)]"}, {"0,0,0,0", "[(0,0),(0,0)]"}, {"-0,-0,-0,-0", "[(-0,-0),(-0,-0)]"}, {"0x1p0,0x1p2,3,2", "[(1,4),(3,2)]"}} {
		if out, err := d.Output(ctx, boxUnitInput(t, d, tc[0])); err != nil || out != tc[1] {
			t.Fatal(tc, out, err)
		}
	}
	for _, input := range []string{"", " ", "bad", "1,2,3", "1,2,3,4,5", "[(1,2)]", "[(1;2),(3,4)]", "[(1,2),(3,4)]junk", "((1,2),(3,4)]", "1,2,3,4, ", "[(1,2),(3,4), ]", "((1,2),(3,4), )", "1,2,3,4,\t", "[(1,2),(3,4),\t]"} {
		_, err := d.Input(ctx, input, -1)
		lsegUnitError(t, err, "22P02", "invalid input syntax for type lseg: \""+input+"\"")
	}
	for _, tc := range [][2]string{{"1e400,2,3,4", "1e400"}, {"1e-4000,2,3,4", "1e-4000"}, {"[(1e400;2),(3,4)]", "1e400"}} {
		_, err := d.Input(ctx, tc[0], -1)
		lsegUnitError(t, err, "22003", "\""+tc[1]+"\" is out of range for type double precision")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := d.Input(cancelled, "1,4,3,2", -1); err != context.Canceled {
		t.Fatal(err)
	}
}

func TestLsegComparison(t *testing.T) {
	types, ctx := testTypes(), context.Background()
	d := types[Lseg]
	ops := []ComparisonOperator{CompareEqual, CompareNotEqual, CompareLess, CompareLessEqual, CompareGreater, CompareGreaterEqual}
	for _, tc := range []struct {
		left, right string
		want        [6]bool
	}{
		{"0,0,3,4", "0,0,3,4", [6]bool{true, false, false, true, false, true}},
		{"0,0,3,4", "3,4,0,0", [6]bool{false, true, false, true, false, true}},
		{"0,0,3,4", "10,10,13,14", [6]bool{false, true, false, true, false, true}},
		{"0,0,3,4", "0,0,0,6", [6]bool{false, true, true, true, false, false}},
		{"0,0,1,0", "0,0,1.0000005,0", [6]bool{true, false, false, true, false, true}},
		{"0,0,1,0", "0,0,1.000002,0", [6]bool{false, true, true, true, false, false}},
		{"NaN,0,1,1", "NaN,0,1,1", [6]bool{true, false, false, false, false, false}},
		{"NaN,1,0,0", "NaN,1.0000005,0,0", [6]bool{false, true, false, false, false, false}},
		{"0,0,Infinity,0", "0,0,Infinity,0", [6]bool{true, false, false, true, false, true}},
		{"Infinity,1,Infinity,0", "Infinity,1,Infinity,0", [6]bool{true, false, false, false, false, false}},
		{"0,0,0,0", "0,0,0,0", [6]bool{true, false, false, true, false, true}},
	} {
		a, b := boxUnitInput(t, d, tc.left), boxUnitInput(t, d, tc.right)
		for i, op := range ops {
			p, err := BindComparisonPredicate(op, d, d, types[Bool], CollationRef{}, false)
			if err != nil {
				t.Fatal(err)
			}
			v, err := p.Evaluate(ctx, a, b, InputEnvironment{})
			if err != nil {
				t.Fatal(tc.left, tc.right, op, err)
			}
			truth, err := v.Boolean()
			if err != nil || truth != tc.want[i] {
				t.Fatal(tc.left, tc.right, op, truth, tc.want[i], err)
			}
			for _, pair := range [][2]Value{{Null(d), b}, {a, Null(d)}, {Null(d), Null(d)}} {
				v, err := p.Evaluate(ctx, pair[0], pair[1], InputEnvironment{})
				if err != nil || !v.IsNull() || v.Type() != types[Bool] {
					t.Fatal("strict BOOL NULL", v, err)
				}
			}
		}
	}
	// NaN's exact rule is per POINT, not per entire segment.
	a, b := boxUnitInput(t, d, "NaN,1,0,0"), boxUnitInput(t, d, "NaN,1,0.0000005,0")
	if truth, err := lsegPredicate(CompareEqual, a, b); err != nil || !truth {
		t.Fatal("per-POINT NaN equality", truth, err)
	}
	huge := boxUnitInput(t, d, "1e308,0,-1e308,0")
	for _, op := range []ComparisonOperator{CompareEqual, CompareNotEqual} {
		truth, err := lsegPredicate(op, huge, huge)
		if err != nil || truth != (op == CompareEqual) {
			t.Fatal("eager length", op, truth, err)
		}
	}
	_, err := lsegPredicate(CompareLess, huge, huge)
	lsegUnitError(t, err, "22003", "value out of range: overflow")
	if distance, err := geometryDistance(0, 0, 1e200, 1e200); err != nil || math.IsInf(distance, 0) || distance <= 1e200 {
		t.Fatal("scaled distance", distance, err)
	}
	if distance, err := geometryDistance(0, 0, math.Inf(1), math.NaN()); err != nil || !math.IsInf(distance, 1) {
		t.Fatal("infinity over NaN", distance, err)
	}
	// Probe both binary64 neighbors of epsilon rather than assuming a decimal
	// boundary is exact after addition/subtraction.
	for _, n := range []float64{math.Nextafter(1e-6, 0), math.Nextafter(1e-6, math.Inf(1))} {
		if got := pointSame(0, 0, n, 0); got != (n <= 1e-6) {
			t.Fatal("epsilon neighbor", n, got)
		}
	}
}

func TestLsegComponents(t *testing.T) {
	types, ctx := testTypes(), context.Background()
	d, point := types[Lseg], types[Point]
	v := boxUnitInput(t, d, "1,4,3,2")
	bound := func(i int32) []ArraySubscriptValue { return []ArraySubscriptValue{{Upper: i, HasUpper: true}} }
	for _, i := range []int32{-1, 0, 1, 2} {
		got, err := ArrayFetch(ctx, v, bound(i))
		if err != nil || got.Type() != point || got.IsNull() != (i < 0 || i > 1) || got.geometry != nil {
			t.Fatal("inline POINT fetch", i, got, err)
		}
		if !got.IsNull() {
			x, y := pointCoordinates(got)
			if x != v.geometry.coordinates[i*2] || y != v.geometry.coordinates[i*2+1] {
				t.Fatal(i, x, y)
			}
		}
	}
	rhs := boxUnitInput(t, point, "(-5,-6)")
	changed, err := ArraySetElement(ctx, v, bound(0), rhs)
	if err != nil || changed.geometry == v.geometry {
		t.Fatal("copy-on-write", err)
	}
	if out, err := d.Output(ctx, changed); err != nil || out != "[(-5,-6),(3,2)]" {
		t.Fatal(out, err)
	}
	if out, err := d.Output(ctx, v); err != nil || out != "[(1,4),(3,2)]" {
		t.Fatal("mutated original", out, err)
	}
	if unchanged, err := ArraySetElement(ctx, changed, bound(0), Null(point)); err != nil || !SameDatum(changed, unchanged) {
		t.Fatal("NULL RHS", err)
	}
	if got, err := ArraySetElement(ctx, Null(d), bound(0), rhs); err != nil || !got.IsNull() {
		t.Fatal("NULL base", err)
	}
	_, err = ArraySetElement(ctx, Null(d), []ArraySubscriptValue{{UpperNull: true}}, Null(point))
	lsegUnitError(t, err, "22004", "array subscript in assignment must not be null")
	if n := testing.AllocsPerRun(10, func() { _, _ = ArrayFetch(ctx, v, []ArraySubscriptValue{{Upper: 0}}) }); n != 0 {
		t.Fatal("POINT fetch allocated", n)
	}
}

func TestLsegExactReplay(t *testing.T) {
	types, ctx := testTypes(), context.Background()
	d := types[Lseg]
	for _, input := range []string{"3,2,1,4", "0.10000000000000002,-0,5e-324,1", "NaN,Infinity,-Infinity,0"} {
		v := boxUnitInput(t, d, input)
		literal, err := d.SQLLiteral(ctx, v)
		if err != nil {
			t.Fatal(err)
		}
		replayed, err := d.Input(ctx, strings.Trim(literal, "'"), -1)
		if err != nil || !SameDatum(v, replayed) {
			t.Fatal("exact replay", input, literal, err)
		}
		if same, err := v.SamePayload(ctx, replayed); err != nil || !same {
			t.Fatal("payload identity", err)
		}
		retagged, err := v.Retag(testTypes()[Lseg])
		if err != nil || retagged.geometry != v.geometry {
			t.Fatal("retag copied payload", err)
		}
	}
	a, b := boxUnitInput(t, d, "0,0,1,0"), boxUnitInput(t, d, "0,0,1.0000005,0")
	if SameDatum(a, b) {
		t.Fatal("epsilon equality became datum identity")
	}
	if same, err := a.SamePayload(ctx, b); err != nil || same {
		t.Fatal("epsilon equality became payload identity", err)
	}
	positive, negative := boxUnitInput(t, d, "0,0,0,0"), boxUnitInput(t, d, "-0,-0,-0,-0")
	if SameDatum(positive, negative) {
		t.Fatal("signed-zero identity erased")
	}
	for i := range 4 {
		x := Value{typ: d, geometry: &geometryPayload{}}
		y := Value{typ: d, geometry: &geometryPayload{}}
		x.geometry.coordinates[i] = math.Float64frombits(0x7ff8000000000001)
		y.geometry.coordinates[i] = math.Float64frombits(0x7ff8000000000002)
		if SameDatum(x, y) {
			t.Fatal("NaN payload identity erased", i)
		}
		if same, err := x.SamePayload(ctx, y); err != nil || same {
			t.Fatal("NaN payload equality", i, err)
		}
	}
}

func TestLsegCapabilities(t *testing.T) {
	types, ctx := testTypes(), context.Background()
	d := types[Lseg]
	if equal, order := ComparisonCapabilities(d); equal || order {
		t.Fatal("default capabilities", equal, order)
	}
	if _, err := BindOrdering(d.Usage()); err == nil {
		t.Fatal("default ordering admitted")
	}
	if _, err := BindGrouping(d.Usage()); err == nil {
		t.Fatal("default grouping admitted")
	}
	if _, ok := DefaultIndexClass(d); ok {
		t.Fatal("default index class admitted")
	}
	if types[1018].Concrete() {
		t.Fatal("companion SQL array admitted")
	}
	for op := CompareEqual; op <= CompareGreaterEqual; op++ {
		if _, err := ResolveComparison(op, d, d); err != nil {
			t.Fatal(op, err)
		}
		p, err := BindComparisonPredicate(op, d, d, types[Bool], CollationRef{}, false)
		if err != nil {
			t.Fatal(op, err)
		}
		if p.EqualityFor(d.Usage()) {
			t.Fatal("hash probe admitted", op)
		}
		if _, err := p.EqualityGrouping(); err == nil {
			t.Fatal("hash grouping admitted", op)
		}
	}
	lsegUnitError(t, d.CheckBinaryInputDeclaration(), "0A000", "binary input is not supported for type lseg")
	lsegUnitError(t, d.CheckBinaryInput(), "0A000", "binary input is not supported for type lseg")
	lsegUnitError(t, d.CheckBinaryOutput(), "0A000", "binary output is not supported for type lseg")
	_, err := d.BinaryInputWithEnvironment(ctx, nil, -1, InputEnvironment{})
	lsegUnitError(t, err, "0A000", "binary input is not supported for type lseg")
	_, err = d.AppendBinaryWithEnvironment(ctx, nil, boxUnitInput(t, d, "1,4,3,2"), OutputEnvironment{})
	lsegUnitError(t, err, "0A000", "binary output is not supported for type lseg")
}
