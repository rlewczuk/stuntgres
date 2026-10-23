package sqltype

import (
	"context"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"stuntgres/internal/sqlstate"
)

func TestCubeCoordinates(t *testing.T) {
	ctx := context.Background()
	d := cubeUnitType(t, 45000)
	for _, tc := range []struct {
		text  string
		op    CubeOperation
		index int32
		want  float64
	}{
		{"(3,1),(1,4)", CubeLowerCoordinate, 1, 1},
		{"(3,1),(1,4)", CubeUpperCoordinate, 1, 3},
		{"(3,1),(1,4)", CubeRawCoordinate, 1, 3},
		{"(3,1),(1,4)", CubeRawCoordinate, 4, 4},
		{"(3,1),(1,4)", CubeSignedCoordinate, -4, -4},
		{"(42,137)", CubeRawCoordinate, 4, 137},
		{"(42,137)", CubeSignedCoordinate, 2, 42},
		{"()", CubeLowerCoordinate, 1, 0},
		{"()", CubeUpperCoordinate, -1, 0},
		{"(1)", CubeLowerCoordinate, math.MinInt32, 0},
		{"(1)", CubeUpperCoordinate, math.MaxInt32, 0},
		{"(" + strings.Repeat("1,", 99) + "2)", CubeRawCoordinate, 200, 2},
		{"(" + strings.Repeat("1,", 99) + "2)", CubeSignedCoordinate, 200, 2},
		{"(" + strings.Repeat("1,", 99) + "2)", CubeSignedCoordinate, 201, 0},
	} {
		v, err := d.Input(ctx, tc.text, -1)
		if err != nil {
			t.Fatal(err)
		}
		before := append([]float64(nil), v.cube.coordinates...)
		got, err := CubeCoordinate(ctx, d, v, tc.op, tc.index)
		if err != nil || got != tc.want {
			t.Fatalf("%s op=%d index=%d: %v, %v", tc.text, tc.op, tc.index, got, err)
		}
		if !reflect.DeepEqual(before, v.cube.coordinates) && len(before) != 0 {
			t.Fatal("mutated source")
		}
	}
	v, err := d.Input(ctx, "(1)", -1)
	if err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []Value{Null(d), {}, {typ: d}, {typ: cubeUnitType(t, d.ID()), cube: v.cube}} {
		if _, err := CubeCoordinate(ctx, d, invalid, CubeRawCoordinate, 1); err == nil {
			t.Fatal("invalid payload accepted")
		}
	}
	if _, err := CubeCoordinate(ctx, nil, v, CubeRawCoordinate, 1); err == nil {
		t.Fatal("invalid descriptor accepted")
	}
	if _, err := CubeCoordinate(ctx, d, v, CubeScalar, 1); err == nil {
		t.Fatal("invalid operation accepted")
	}
}

func TestCubeCoordinateBits(t *testing.T) {
	ctx, d := context.Background(), cubeUnitType(t, 45000)
	for _, tc := range []struct {
		text  string
		op    CubeOperation
		index int32
		want  uint64
	}{
		{"(-0,1),(0,2)", CubeRawCoordinate, 1, 1 << 63},
		{"(-0,1),(0,2)", CubeLowerCoordinate, 1, 0},
		{"(-0,1),(0,2)", CubeUpperCoordinate, 1, 0},
		{"(0,2),(-0,1)", CubeLowerCoordinate, 1, 1 << 63},
		{"(0,2),(-0,1)", CubeUpperCoordinate, 1, 1 << 63},
		{"()", CubeSignedCoordinate, -1, 1 << 63},
		{"(1)", CubeSignedCoordinate, math.MinInt32, 1 << 63},
		{"(1)", CubeSignedCoordinate, math.MaxInt32, 0},
		{"(1),(Infinity)", CubeSignedCoordinate, -2, 0xfff0000000000000},
		{"(NaN),(1)", CubeLowerCoordinate, 1, 0x3ff0000000000000},
		{"(NaN),(1)", CubeUpperCoordinate, 1, 0x3ff0000000000000},
		{"(5e-324)", CubeRawCoordinate, 1, 1},
		{"(0.10000000000000002)", CubeRawCoordinate, 1, 0x3fb999999999999b},
		{"(1.7976931348623157e308)", CubeRawCoordinate, 1, 0x7fefffffffffffff},
	} {
		v, err := d.Input(ctx, tc.text, -1)
		if err != nil {
			t.Fatal(err)
		}
		got, err := CubeCoordinate(ctx, d, v, tc.op, tc.index)
		if err != nil || math.Float64bits(got) != tc.want {
			t.Fatalf("%s op=%d index=%d: %016x want %016x, %v", tc.text, tc.op, tc.index, math.Float64bits(got), tc.want, err)
		}
	}
	v, err := d.Input(ctx, "(1),(NaN)", -1)
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range []CubeOperation{CubeLowerCoordinate, CubeUpperCoordinate} {
		got, err := CubeCoordinate(ctx, d, v, op, 1)
		if err != nil || !math.IsNaN(got) {
			t.Fatal("ordered NaN", got, err)
		}
	}
	positive, err := CubeCoordinate(ctx, d, v, CubeSignedCoordinate, 1)
	if err != nil {
		t.Fatal(err)
	}
	negative, err := CubeCoordinate(ctx, d, v, CubeSignedCoordinate, -1)
	if err != nil || math.Float64bits(positive)^math.Float64bits(negative) != 1<<63 {
		t.Fatal("NaN arithmetic sign inversion", err)
	}
}

func TestCubeCoordinateDiagnostics(t *testing.T) {
	ctx, d := context.Background(), cubeUnitType(t, 45000)
	v, err := d.Input(ctx, "(1)", -1)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		op      CubeOperation
		index   int32
		message string
	}{
		{CubeRawCoordinate, 0, "cube index 0 is out of bounds"},
		{CubeRawCoordinate, -1, "cube index -1 is out of bounds"},
		{CubeRawCoordinate, 3, "cube index 3 is out of bounds"},
		{CubeRawCoordinate, math.MinInt32, "cube index -2147483648 is out of bounds"},
		{CubeRawCoordinate, math.MaxInt32, "cube index 2147483647 is out of bounds"},
		{CubeSignedCoordinate, 0, "zero cube index is not defined"},
	} {
		_, err := CubeCoordinate(ctx, d, v, tc.op, tc.index)
		var got *sqlstate.Error
		want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2202E", Message: tc.message}
		if !errors.As(err, &got) || !reflect.DeepEqual(got, want) {
			t.Fatal("diagnostic", err, want)
		}
	}
	empty, err := d.Input(ctx, "()", -1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CubeCoordinate(ctx, d, empty, CubeRawCoordinate, 1); err == nil {
		t.Fatal("empty raw coordinate accepted")
	}
}

func TestCubeCoordinateCancellation(t *testing.T) {
	d := cubeUnitType(t, 45000)
	v, err := d.Input(context.Background(), "(1)", -1)
	if err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	expired, finish := context.WithDeadline(context.Background(), time.Unix(0, 0))
	defer finish()
	for _, tc := range []struct {
		ctx  context.Context
		want error
	}{{canceled, context.Canceled}, {expired, context.DeadlineExceeded}} {
		for op := CubeLowerCoordinate; op <= CubeSignedCoordinate; op++ {
			if _, err := CubeCoordinate(tc.ctx, d, v, op, 1); !errors.Is(err, tc.want) {
				t.Fatal("context error", err)
			}
		}
	}
}

func TestCubeCoordinateAllocation(t *testing.T) {
	d := cubeUnitType(t, 45000)
	ctx := context.Background()
	v, err := d.Input(ctx, "(3,1),(1,4)", -1)
	if err != nil {
		t.Fatal(err)
	}
	for op := CubeLowerCoordinate; op <= CubeSignedCoordinate; op++ {
		result := testing.Benchmark(func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				got, err := CubeCoordinate(ctx, d, v, op, 1)
				if err != nil || got == 0 {
					b.Fatal(got, err)
				}
			}
		})
		if result.AllocsPerOp() != 0 || result.AllocedBytesPerOp() != 0 {
			t.Fatal("coordinate extraction allocated", op, result)
		}
	}
}
