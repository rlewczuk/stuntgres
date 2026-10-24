package sqltype

import (
	"context"
	"errors"
	"math"
	"reflect"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestGeometryTransformSignatures(t *testing.T) {
	types := testTypes()
	for _, op := range []ArithmeticOperator{ArithmeticAdd, ArithmeticSubtract, ArithmeticMultiply, ArithmeticDivide} {
		for _, left := range []ID{Point, Box, Path, Circle, Line, Lseg, Polygon, Int4, Numeric} {
			for _, right := range []ID{Point, Box, Path, Circle, Line, Lseg, Polygon, Int4, Numeric} {
				// Numeric/numeric is an existing independent arithmetic family.
				if (left == Int4 || left == Numeric) && (right == Int4 || right == Numeric) {
					continue
				}
				admitted := right == Point && (left == Point || left == Box || left == Path || left == Circle) || op == ArithmeticAdd && left == Path && right == Path
				s, err := ResolveArithmetic(op, types[left], types[right], false)
				if !admitted {
					if err == nil {
						t.Fatalf("accepted %v %v %v", left, op, right)
					}
					continue
				}
				if err != nil || s != (ArithmeticSignature{left, right, left}) {
					t.Fatalf("signature %v %v %v: %v %v", left, op, right, s, err)
				}
				if _, err := BindArithmetic(op, types[left], types[right], types[left]); err != nil {
					t.Fatal(err)
				}
				if _, err := BindArithmetic(op, types[left], types[right], types[Bool]); err == nil {
					t.Fatal("accepted wrong result")
				}
			}
		}
	}
	for _, tc := range []struct {
		op    ArithmeticOperator
		right ID
	}{{ArithmeticAdd, Path}, {ArithmeticMultiply, Point}} {
		s, err := ResolveArithmetic(tc.op, types[Path], nil, false)
		if err != nil || s.Right != tc.right {
			t.Fatal(s, err)
		}
	}
	for _, id := range []ID{Point, Box, Path, Circle} {
		for _, op := range []ArithmeticOperator{ArithmeticAdd, ArithmeticSubtract} {
			if _, err := ResolveArithmetic(op, types[id], nil, true); err == nil {
				t.Fatal("accepted geometric unary")
			}
			if _, err := BindArithmetic(op, types[id], nil, types[id]); err == nil {
				t.Fatal("bound geometric unary")
			}
		}
	}
	if _, err := BindArithmetic(ArithmeticAdd, nil, types[Point], types[Point]); err == nil {
		t.Fatal("nil descriptor")
	}
	if _, err := BindArithmetic(ArithmeticPower+1, types[Point], types[Point], types[Point]); err == nil {
		t.Fatal("invalid operator")
	}
	p, _ := BindArithmetic(ArithmeticAdd, types[Point], types[Point], types[Point])
	foreign := testTypes()[Point]
	if _, err := p.Binary(context.Background(), pointValue(foreign, 1, 2), pointValue(types[Point], 3, 4), InputEnvironment{}); err == nil {
		t.Fatal("foreign descriptor")
	}
}

func TestGeometryTransformCoordinates(t *testing.T) {
	for _, tc := range []struct {
		op               ArithmeticOperator
		x, y, u, v, a, b float64
	}{
		{ArithmeticAdd, 1, 2, 3, 4, 4, 6}, {ArithmeticSubtract, 1, 2, 3, 4, -2, -2},
		{ArithmeticMultiply, 1, 2, 3, 4, -5, 10}, {ArithmeticDivide, 1, 2, 1, 1, 1.5, .5},
		{ArithmeticMultiply, 1e-150, 0, 1e-160, 0, 1e-310, 0},
	} {
		a, b, err := geometryTransformPoint(tc.op, tc.x, tc.y, tc.u, tc.v)
		if err != nil || math.Float64bits(a) != math.Float64bits(tc.a) || math.Float64bits(b) != math.Float64bits(tc.b) {
			t.Fatal(tc, a, b, err)
		}
	}
	types := testTypes()
	p, _ := BindArithmetic(ArithmeticAdd, types[Circle], types[Point], types[Circle])
	radius := math.Copysign(0, -1)
	a := Value{typ: types[Circle], circle: circleDatum{1, 2, radius}}
	v, err := p.Binary(context.Background(), a, pointValue(types[Point], 3, 4), InputEnvironment{})
	if err != nil || math.Float64bits(v.circle.radius) != math.Float64bits(radius) || a.circle.x != 1 {
		t.Fatal(v, err)
	}
	v, err = p.Binary(context.Background(), Null(types[Circle]), pointValue(types[Point], 0, 0), InputEnvironment{})
	if err != nil || !v.null || v.typ != types[Circle] {
		t.Fatal(v, err)
	}
}

func TestGeometryTransformCorners(t *testing.T) {
	types := testTypes()
	zero := math.Copysign(0, -1)
	for _, coords := range [][4]float64{{-5, -6, 1, 2}, {zero, 0, 0, zero}, {0, math.NaN(), math.NaN(), 1}} {
		a := Value{typ: types[Box], geometry: &geometryPayload{coordinates: coords}}
		p, _ := BindArithmetic(ArithmeticMultiply, types[Box], types[Point], types[Box])
		v, err := p.Binary(context.Background(), a, pointValue(types[Point], 1, 0), InputEnvironment{})
		if err != nil {
			t.Fatal(err)
		}
		if v.geometry == a.geometry {
			t.Fatal("reused BOX payload")
		}
		for i := range coords {
			if math.Float64bits(a.geometry.coordinates[i]) != math.Float64bits(coords[i]) {
				t.Fatal("mutated BOX")
			}
		}
		if compareFloats(v.geometry.coordinates[0], v.geometry.coordinates[2]) < 0 || compareFloats(v.geometry.coordinates[1], v.geometry.coordinates[3]) < 0 {
			t.Fatal("unnormalized BOX")
		}
	}
	// Zero multiplication preserves signs through the checked operations, then
	// equal axis values swap exactly as box_construct does.
	a := Value{typ: types[Box], geometry: &geometryPayload{coordinates: [4]float64{zero, zero, 0, 0}}}
	p, _ := BindArithmetic(ArithmeticDivide, types[Box], types[Point], types[Box])
	v, err := p.Binary(context.Background(), a, pointValue(types[Point], 1, 0), InputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	if math.Signbit(v.geometry.coordinates[0]) || !math.Signbit(v.geometry.coordinates[2]) {
		t.Fatal("equal-axis tie did not swap", v.geometry.coordinates)
	}
}

func TestGeometryTransformPaths(t *testing.T) {
	types := testTypes()
	ctx := context.Background()
	points := [][2]float64{{1, 2}, {1, 2}, {3, 4}}
	a := Value{typ: types[Path], path: &pathPayload{closed: true, points: points}}
	p, _ := BindArithmetic(ArithmeticMultiply, types[Path], types[Point], types[Path])
	v, err := p.Binary(ctx, a, pointValue(types[Point], 0, 2), InputEnvironment{})
	if err != nil || !v.path.closed || !reflect.DeepEqual(v.path.points, [][2]float64{{-4, 2}, {-4, 2}, {-8, 6}}) {
		t.Fatal(v, err)
	}
	if !reflect.DeepEqual(points, [][2]float64{{1, 2}, {1, 2}, {3, 4}}) {
		t.Fatal("mutated path")
	}
	p, _ = BindArithmetic(ArithmeticAdd, types[Path], types[Path], types[Path])
	v, err = p.Binary(ctx, a, a, InputEnvironment{})
	if err != nil || !v.null || v.typ != types[Path] {
		t.Fatal(v, err)
	}
	a.path = &pathPayload{points: points}
	v, err = p.Binary(ctx, a, a, InputEnvironment{})
	if err != nil || v.path.closed || len(v.path.points) != 6 {
		t.Fatal(v, err)
	}
	v.path.points[0][0] = 99
	if points[0][0] != 1 {
		t.Fatal("concat aliases operand")
	}
	const max = (0x7fffffff - 16) / 16
	for _, tc := range []struct {
		a, b int
		ok   bool
	}{{1, 2, true}, {max, 0, true}, {max - 1, 1, true}, {max, 1, false}, {max + 1, 0, false}, {-1, 1, false}} {
		n, err := geometryConcatCount(tc.a, tc.b)
		if tc.ok {
			if err != nil || n != tc.a+tc.b {
				t.Fatal(tc, n, err)
			}
		} else {
			var d *sqlstate.Error
			if !errors.As(err, &d) || d.Code != "54000" || d.Message != "too many points requested" {
				t.Fatal(tc, err)
			}
		}
	}
	p, _ = BindArithmetic(ArithmeticDivide, types[Path], types[Point], types[Path])
	points = [][2]float64{{0, 0}, {1e308, 1}}
	a.path = &pathPayload{points: points}
	_, err = p.Binary(ctx, a, pointValue(types[Point], .5, 0), InputEnvironment{})
	if err == nil || points[0] != ([2]float64{0, 0}) || points[1] != ([2]float64{1e308, 1}) {
		t.Fatal("failed traversal changed source", err)
	}
}

func TestGeometryTransformNumericEdges(t *testing.T) {
	for _, tc := range []struct {
		op         ArithmeticOperator
		x, y, u, v float64
		message    string
	}{
		{ArithmeticDivide, 1, 2, 0, 0, "division by zero"},
		{ArithmeticDivide, 1, 2, 1e200, 0, "value out of range: overflow"},
		{ArithmeticDivide, 1, 2, 1e-200, 0, "value out of range: underflow"},
		{ArithmeticMultiply, math.NaN(), 1e308, 0, 2, "value out of range: overflow"},
	} {
		_, _, err := geometryTransformPoint(tc.op, tc.x, tc.y, tc.u, tc.v)
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Message != tc.message {
			t.Fatal(tc, err)
		}
	}
	x, y, err := geometryTransformPoint(ArithmeticDivide, math.NaN(), 1, 0, 0)
	if err != nil || !math.IsNaN(x) || !math.IsNaN(y) {
		t.Fatal(x, y, err)
	}
	for _, tc := range []struct{ x, y, want float64 }{{math.Inf(1), math.NaN(), math.Inf(1)}, {math.NaN(), 1, math.NaN()}, {math.Copysign(0, -1), 0, 0}, {1e150, 1e-200, 1e150}} {
		n, err := geometryHypot(tc.x, tc.y)
		if err != nil || !(math.IsNaN(n) && math.IsNaN(tc.want)) && math.Float64bits(n) != math.Float64bits(tc.want) {
			t.Fatal(tc, n, err)
		}
	}
}

type geometryTransformCancelContext struct {
	context.Context
	remaining int
}

func (c *geometryTransformCancelContext) Err() error {
	c.remaining--
	if c.remaining <= 0 {
		return context.Canceled
	}
	return nil
}

func TestGeometryTransformCancellation(t *testing.T) {
	types := testTypes()
	points := make([][2]float64, 100)
	for i := range points {
		points[i] = [2]float64{float64(i), 1}
	}
	a := Value{typ: types[Path], path: &pathPayload{points: points}}
	for _, concat := range []bool{false, true} {
		right := pointValue(types[Point], 1, 2)
		op := ArithmeticMultiply
		if concat {
			right = a
			op = ArithmeticAdd
		}
		p, _ := BindArithmetic(op, types[Path], right.typ, types[Path])
		ctx := &geometryTransformCancelContext{Context: context.Background(), remaining: 10}
		v, err := p.Binary(ctx, a, right, InputEnvironment{})
		if !errors.Is(err, context.Canceled) || v.typ != nil {
			t.Fatal("partial result", v, err)
		}
		for i, point := range points {
			if point != ([2]float64{float64(i), 1}) {
				t.Fatal("canceled traversal mutated source")
			}
		}
		canceled, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := p.Binary(canceled, Null(types[Path]), right, InputEnvironment{}); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	}
}
