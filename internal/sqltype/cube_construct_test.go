package sqltype

import (
	"context"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestCubeConstructors(t *testing.T) {
	ctx := context.Background()
	cube, types := cubeUnitType(t, 45000), testTypes()
	float := func(n float64) Value {
		v, err := Float(types[Float8], n)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	array := func(text string) Value {
		v, err := types[1022].Input(ctx, text, -1)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	base := func(text string) Value {
		v, err := cube.Input(ctx, text, -1)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	for _, tc := range []struct {
		name      string
		op        CubeOperation
		args      []Value
		dimension int
		point     bool
		bits      []uint64
	}{
		{"scalar", CubeScalar, []Value{float(1)}, 1, true, []uint64{0x3ff0000000000000}},
		{"reversed", CubeScalars, []Value{float(2), float(1)}, 1, false, []uint64{0x4000000000000000, 0x3ff0000000000000}},
		{"signed_zero", CubeScalars, []Value{float(math.Copysign(0, -1)), float(0)}, 1, true, []uint64{1 << 63}},
		{"array", CubeArray, []Value{array("[-1:0][3:4]={{1,2},{3,4}}")}, 4, true, []uint64{0x3ff0000000000000, 0x4000000000000000, 0x4008000000000000, 0x4010000000000000}},
		{"arrays", CubeArrays, []Value{array("{1,2}"), array("{3,4}")}, 2, false, []uint64{0x3ff0000000000000, 0x4000000000000000, 0x4008000000000000, 0x4010000000000000}},
		{"extend_box", CubeExtend, []Value{base("(1),(2)"), float(3)}, 2, false, []uint64{0x3ff0000000000000, 0x4008000000000000, 0x4000000000000000, 0x4008000000000000}},
		{"extend_pair", CubeExtendPair, []Value{base("1"), float(2), float(3)}, 2, false, []uint64{0x3ff0000000000000, 0x4000000000000000, 0x3ff0000000000000, 0x4008000000000000}},
		{"extend_equal", CubeExtendPair, []Value{base("1"), float(2), float(2)}, 2, true, []uint64{0x3ff0000000000000, 0x4000000000000000}},
		{"empty", CubeArray, []Value{array("{}")}, 0, true, []uint64{}},
		{"two_empty", CubeArrays, []Value{array("{}"), array("{}")}, 0, true, []uint64{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v, err := ConstructCube(ctx, cube, tc.op, tc.args)
			if err != nil {
				t.Fatal(err)
			}
			dim, point, err := CubeInfo(cube, v)
			if err != nil || dim != tc.dimension || point != tc.point || len(v.cube.coordinates) != len(tc.bits) {
				t.Fatal("cube shape", dim, point, v.cube, err)
			}
			for i, bits := range tc.bits {
				if math.Float64bits(v.cube.coordinates[i]) != bits {
					t.Fatal("coordinate bits", i, v.cube.coordinates[i], bits)
				}
			}
		})
	}
	for _, op := range []CubeOperation{CubeScalar, CubeScalars, CubeExtend, CubeExtendPair} {
		args := []Value{float(math.NaN())}
		wantPoint := true
		switch op {
		case CubeScalars:
			args, wantPoint = append(args, float(math.NaN())), false
		case CubeExtend, CubeExtendPair:
			args = []Value{base("NaN"), float(math.NaN())}
			if op == CubeExtendPair {
				args, wantPoint = append(args, float(math.NaN())), false
			}
		}
		v, err := ConstructCube(ctx, cube, op, args)
		if err != nil || v.cube.point != wantPoint || !math.IsNaN(v.cube.coordinates[0]) {
			t.Fatal("IEEE NaN point flag", op, v, err)
		}
	}
	input := base("(" + strings.Repeat("1,", 98) + "1)")
	v, err := ConstructCube(ctx, cube, CubeExtend, []Value{input, float(2)})
	if err != nil || v.cube.dimension != 100 || input.cube.dimension != 99 || v.cube.coordinates[99] != 2 || &v.cube.coordinates[0] == &input.cube.coordinates[0] {
		t.Fatal("extension buffer ownership", err)
	}
	for _, op := range []CubeOperation{CubeScalar, CubeScalars, CubeArray, CubeArrays, CubeExtend, CubeExtendPair} {
		args := []Value{float(1)}
		switch op {
		case CubeScalars:
			args = append(args, float(2))
		case CubeArray:
			args = []Value{array("{1}")}
		case CubeArrays:
			args = []Value{array("{1}"), array("{2}")}
		case CubeExtend:
			args = []Value{base("1"), float(2)}
		case CubeExtendPair:
			args = []Value{base("1"), float(2), float(3)}
		}
		for i, original := range args {
			args[i] = Null(original.Type())
			v, err := ConstructCube(ctx, cube, op, args)
			if err != nil || !v.IsNull() || v.Type() != cube {
				t.Fatal("typed strict NULL", op, i, err)
			}
			args[i] = original
		}
	}
	foreign := cubeUnitType(t, cube.ID())
	if _, err := ConstructCube(ctx, foreign, CubeExtend, []Value{base("1"), float(2)}); err == nil {
		t.Fatal("foreign cube generation accepted")
	}
	if _, _, err := CubeInfo(foreign, base("1")); err == nil {
		t.Fatal("foreign inquiry generation accepted")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := ConstructCube(cancelled, cube, CubeScalar, []Value{float(1)}); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation", err)
	}
	// External source buffers and array payloads cannot become mutable aliases
	// of a constructed cube's coordinate storage.
	elements := []Value{float(1), float(2)}
	ownedArray, err := ArrayValue(types[1022], []ArrayDimension{{Lower: -2, Length: 2}}, elements)
	if err != nil {
		t.Fatal(err)
	}
	v, err = ConstructCube(ctx, cube, CubeArray, []Value{ownedArray})
	if err != nil {
		t.Fatal(err)
	}
	elements[0] = float(9)
	if v.cube.coordinates[0] != 1 || ownedArray.ArrayElement(0).floating != 1 {
		t.Fatal("borrowed array buffer")
	}
}

func TestCubeConstructorDiagnostics(t *testing.T) {
	ctx, types := context.Background(), testTypes()
	cube := cubeUnitType(t, 45000)
	array := func(text string) Value {
		v, err := types[1022].Input(ctx, text, -1)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	a100, a101 := array("{"+strings.Repeat("1,", 99)+"1}"), array("{"+strings.Repeat("1,", 100)+"1}")
	nulls := array("{" + strings.Repeat("1,", 100) + "NULL}")
	base, err := ConstructCube(ctx, cube, CubeArray, []Value{a100})
	if err != nil {
		t.Fatal(err)
	}
	floating, err := Float(types[Float8], 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		op                    CubeOperation
		args                  []Value
		code, message, detail string
	}{
		{CubeArray, []Value{a101}, "54000", "array is too long", "A cube cannot have more than 100 dimensions."},
		{CubeArrays, []Value{a101, a100}, "54000", "can't extend cube", "A cube cannot have more than 100 dimensions."},
		{CubeArrays, []Value{a100, a101}, "2202E", "UR and LL arrays must be of same length", ""},
		{CubeArray, []Value{nulls}, "2202E", "cannot work with arrays containing NULLs", ""},
		{CubeArrays, []Value{a101, nulls}, "2202E", "cannot work with arrays containing NULLs", ""},
		{CubeArrays, []Value{nulls, a100}, "2202E", "cannot work with arrays containing NULLs", ""},
		{CubeExtend, []Value{base, floating}, "54000", "can't extend cube", "A cube cannot have more than 100 dimensions."},
		{CubeExtendPair, []Value{base, floating, floating}, "54000", "can't extend cube", "A cube cannot have more than 100 dimensions."},
	} {
		_, err := ConstructCube(ctx, cube, tc.op, tc.args)
		var got *sqlstate.Error
		want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message, Detail: tc.detail}
		if !errors.As(err, &got) || !reflect.DeepEqual(got, want) {
			t.Fatalf("%v: got %#v want %#v", tc.op, err, want)
		}
	}
	for _, args := range [][]Value{{a101, Null(types[1022])}, {Null(types[1022]), nulls}} {
		v, err := ConstructCube(ctx, cube, CubeArrays, args)
		if err != nil || !v.IsNull() {
			t.Fatal("whole NULL precedes kernel errors", err)
		}
	}
}
