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

func cubeTransformInputs(t *testing.T) (*Descriptor, func(string) Value, func(string) Value, func(float64, int64) []Value) {
	t.Helper()
	d, types := cubeUnitType(t, 45000), testTypes()
	input := func(d *Descriptor, text string) Value {
		v, err := d.Input(context.Background(), text, -1)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	return d, func(s string) Value { return input(d, s) }, func(s string) Value { return input(types[1007], s) }, func(r float64, n int64) []Value {
		radius, err := Float(types[Float8], r)
		if err != nil {
			t.Fatal(err)
		}
		count, err := Integer(types[Int4], n)
		if err != nil {
			t.Fatal(err)
		}
		return []Value{radius, count}
	}
}

func TestCubeTransformValues(t *testing.T) {
	d, cube, array, enlarge := cubeTransformInputs(t)
	for _, tc := range []struct {
		name string
		op CubeOperation
		args []Value
		want []float64
		point bool
	}{
		{"subset", CubeSubset, []Value{cube("(1,3,5),(6,7,8)"), array("{{3,2},{1,1}}")}, []float64{5,3,1,1,8,7,6,6}, false},
		{"reversed", CubeSubset, []Value{cube("(6,7,8),(1,3,5)"), array("{3,2,1,1}")}, []float64{8,7,6,6,5,3,1,1}, false},
		{"point", CubeSubset, []Value{cube("(1,3,5)"), array("[0:1]={2,1}")}, []float64{3,1}, true},
		{"empty_box", CubeSubset, []Value{cube("(1),(2)"), array("{}")}, []float64{}, false},
		{"coincident_box", CubeSubset, []Value{cube("(1,2),(1,3)"), array("{1}")}, []float64{1,1}, false},
		{"empty_point", CubeSubset, []Value{cube("()"), array("{}")}, []float64{}, true},
		{"union", CubeUnion, []Value{cube("(1,2),(3,4)"), cube("(5,6,7),(8,9,10)")}, []float64{1,2,0,8,9,10}, false},
		{"union_reverse", CubeUnion, []Value{cube("(5,6,7),(8,9,10)"), cube("(1,2),(3,4)")}, []float64{1,2,0,8,9,10}, false},
		{"disjoint", CubeIntersection, []Value{cube("(1,2)"), cube("(3,4)")}, []float64{3,4,1,2}, false},
		{"touch", CubeIntersection, []Value{cube("(1,2),(10,11)"), cube("(10,11),(12,13)")}, []float64{10,11}, true},
		{"padding", CubeIntersection, []Value{cube("(0,-1),(1,1)"), cube("(-2),(2)")}, []float64{0,0,1,0}, false},
		{"zero", CubeUnion, []Value{cube("()"), cube("()")}, []float64{}, true},
		{"grow", CubeEnlarge, append([]Value{cube("(1,2),(3,4)")}, enlarge(.5,3)...), []float64{.5,1.5,-.5,3.5,4.5,.5}, false},
		{"shrink", CubeEnlarge, append([]Value{cube("(2,-2),(-3,7)")}, enlarge(-3,2)...), []float64{-.5,1,-.5,4}, false},
		{"collapse", CubeEnlarge, append([]Value{cube("(1,2),(3,4)")}, enlarge(-2,0)...), []float64{2,3}, true},
		{"normalize", CubeEnlarge, append([]Value{cube("(2),(-2)")}, enlarge(0,4)...), []float64{-2,2}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v, err := TransformCube(context.Background(), d, tc.op, tc.args)
			if err != nil || v.Type() != d || v.cube.point != tc.point || !reflect.DeepEqual(v.cube.coordinates, tc.want) {
				t.Fatalf("result %#v, %v; want %v point %v", v.cube, err, tc.want, tc.point)
			}
			wantDimension := len(tc.want)
			if !tc.point {
				wantDimension /= 2
			}
			if dimension, err := CubeDimensionCount(d, v); err != nil || dimension != wantDimension {
				t.Fatal("stored dimension inquiry", dimension, wantDimension, err)
			}
			if tc.name == "empty_box" || tc.name == "coincident_box" {
				_, point, err := CubeInfo(d,v)
				if err != nil || !point || v.cube.point {
					t.Fatal("geometric inquiry changed storage flag",err)
				}
				want := "()"
				if tc.name == "coincident_box" { want = "(1)" }
				text, err := d.Output(context.Background(),v)
				if err != nil || text != want || v.cube.point {
					t.Fatal("geometric output changed storage flag",text,err)
				}
			}
			for i, original := range tc.args {
				args := append([]Value(nil), tc.args...)
				args[i] = Null(original.Type())
				v, err := TransformCube(context.Background(), d, tc.op, args)
				if err != nil || !v.IsNull() || v.Type() != d {
					t.Fatal("strict typed NULL", i, err)
				}
			}
		})
	}
	for _, n := range []int64{100,101,2147483647} {
		v, err := TransformCube(context.Background(), d, CubeEnlarge, append([]Value{cube("()")}, enlarge(1,n)...))
		if err != nil || v.cube.dimension != 100 || v.cube.coordinates[99] != -1 || v.cube.coordinates[199] != 1 {
			t.Fatal("clamped dimensions", n, err)
		}
	}
	v, err := TransformCube(context.Background(), d, CubeSubset, []Value{cube("1"), array("{"+strings.Repeat("1,",99)+"1}")})
	if err != nil || v.cube.dimension != 100 || !v.cube.point {
		t.Fatal("100 repeated dimensions", err)
	}
}

func TestCubeTransformDiagnostics(t *testing.T) {
	d, cube, array, _ := cubeTransformInputs(t)
	for _, tc := range []struct { indexes, code, message, detail string }{
		{"{NULL}","2202E","cannot work with arrays containing NULLs",""},
		{"{0,"+strings.Repeat("1,",99)+"NULL}","2202E","cannot work with arrays containing NULLs",""},
		{"{0,"+strings.Repeat("1,",99)+"1}","54000","array is too long","A cube cannot have more than 100 dimensions."},
		{"{0}","2202E","Index out of bounds",""},
		{"{-2147483648}","2202E","Index out of bounds",""},
		{"{2147483647}","2202E","Index out of bounds",""},
	} {
		v, err := TransformCube(context.Background(), d, CubeSubset, []Value{cube("1"),array(tc.indexes)})
		var got *sqlstate.Error
		want := &sqlstate.Error{Severity:"ERROR",SeverityUnlocalized:"ERROR",Code:tc.code,Message:tc.message,Detail:tc.detail}
		if !errors.As(err,&got) || !reflect.DeepEqual(got,want) || v.cube != nil || v.Type() != nil {
			t.Fatalf("diagnostic %v %#v",err,v)
		}
	}
}

func TestCubeTransformIEEE(t *testing.T) {
	d, cube, array, enlarge := cubeTransformInputs(t)
	input := cube("(-0,0.10000000000000002)")
	before := append([]float64(nil), input.cube.coordinates...)
	v, err := TransformCube(context.Background(),d,CubeSubset,[]Value{input,array("{2,1}")})
	if err != nil || math.Float64bits(v.cube.coordinates[0]) != 0x3fb999999999999b || math.Float64bits(v.cube.coordinates[1]) != 1<<63 {
		t.Fatal("exact bits",v,err)
	}
	for i, x := range before {
		if math.Float64bits(input.cube.coordinates[i]) != math.Float64bits(x) { t.Fatal("mutated input") }
	}
	zeroBox := cube("(-0,1),(0,2)")
	zero, err := TransformCube(context.Background(),d,CubeSubset,[]Value{zeroBox,array("{1}")})
	if err != nil || zero.cube.point || !zero.cube.isPoint() || math.Float64bits(zero.cube.coordinates[0]) != 1<<63 || math.Float64bits(zero.cube.coordinates[1]) != 0 {
		t.Fatal("signed-zero subset storage",err)
	}
	if text, err := d.Output(context.Background(),zero); err != nil || text != "(-0)" {
		t.Fatal("signed-zero geometric output",text,err)
	}
	nan, err := TransformCube(context.Background(),d,CubeSubset,[]Value{cube("(NaN,1),(NaN,2)"),array("{1}")})
	if err != nil || nan.cube.point || nan.cube.isPoint() || !math.IsNaN(nan.cube.coordinates[0]) || !math.IsNaN(nan.cube.coordinates[1]) {
		t.Fatal("NaN subset geometry",err)
	}
	for _, r := range []float64{math.NaN(), math.Inf(-1)} {
		v, err := TransformCube(context.Background(),d,CubeEnlarge,append([]Value{cube("(1),(2)")},enlarge(r,3)...))
		if err != nil || v.cube.dimension != 1 || v.cube.point || !math.IsNaN(v.cube.coordinates[0]) || !math.IsNaN(v.cube.coordinates[1]) { t.Fatal("NaN bounds",v,err) }
	}
	v, err = TransformCube(context.Background(),d,CubeEnlarge,append([]Value{cube("(1),(2)")},enlarge(math.Inf(1),2)...))
	if err != nil || v.cube.dimension != 2 || !math.IsInf(v.cube.coordinates[0],-1) || !math.IsInf(v.cube.coordinates[3],1) { t.Fatal("infinite bounds",err) }
	for _, tc := range []struct { a,b string; nan bool; want float64 }{{"NaN","1",false,1},{"1","NaN",true,0}} {
		v, err := TransformCube(context.Background(),d,CubeUnion,[]Value{cube(tc.a),cube(tc.b)})
		if err != nil || math.IsNaN(v.cube.coordinates[0]) != tc.nan || !tc.nan && v.cube.coordinates[0] != tc.want { t.Fatal("ordered NaN union",err,v) }
	}
	v, err = TransformCube(context.Background(),d,CubeEnlarge,append([]Value{cube("1.7e308")},enlarge(-1e308,1)...))
	if err != nil || !math.IsInf(v.cube.coordinates[0],1) { t.Fatal("direct average overflow",v,err) }
}

func TestCubeTransformIdentity(t *testing.T) {
	d, cube, array, _ := cubeTransformInputs(t)
	input := cube("(1),(2)")
	v, err := TransformCube(context.Background(),d,CubeSubset,[]Value{input,array("{1}")})
	if err != nil || v.cube == input.cube || &v.cube.coordinates[0] == &input.cube.coordinates[0] { t.Fatal("borrowed payload",err) }
	foreign := cubeUnitType(t,d.ID())
	remapped, err := v.RemapCube(foreign)
	if err != nil || remapped.Type() != foreign || v.Type() != d || input.cube.coordinates[0] != 1 || input.cube.coordinates[1] != 2 {
		t.Fatal("immutable remap", err)
	}
	for _, tc := range []struct { d *Descriptor; op CubeOperation; args []Value }{
		{foreign,CubeSubset,[]Value{input,array("{1}")}},
		{nil,CubeSubset,[]Value{input,array("{1}")}},
		{testTypes()[Int4],CubeUnion,[]Value{input,input}},
		{d,CubeUnion,[]Value{{typ:d,cube:&cubePayload{dimension:1,point:false}},input}},
		{d,CubeScalar,[]Value{input}},
		{d,CubeUnion,[]Value{input}},
		{d,CubeUnion,[]Value{Null(d),Null(foreign)}},
		{d,CubeSubset,[]Value{input,Null(testTypes()[1022])}},
	} {
		if _, err := TransformCube(context.Background(),tc.d,tc.op,tc.args); err == nil { t.Fatal("invalid signature accepted") }
	}
	for _, tc := range []struct{ descriptor *Descriptor; value Value }{
		{nil, input},
		{foreign, input},
		{testTypes()[Int4], input},
		{d, Null(d)},
		{d, Value{typ:d,cube:&cubePayload{dimension:1,point:false}}},
	} {
		if _, err := CubeDimensionCount(tc.descriptor, tc.value); err == nil {
			t.Fatal("invalid dimension inquiry accepted")
		}
		if _, _, err := CubeInfo(tc.descriptor, tc.value); err == nil {
			t.Fatal("invalid geometric inquiry accepted")
		}
	}
}

func TestCubeTransformCancellation(t *testing.T) {
	d,cube,array,_ := cubeTransformInputs(t)
	cancelled,cancel := context.WithCancel(context.Background())
	cancel()
	expired,done := context.WithDeadline(context.Background(),time.Unix(0,0))
	defer done()
	for _, ctx := range []context.Context{cancelled,expired} {
		_,err := TransformCube(ctx,d,CubeSubset,[]Value{cube("1"),array("{1}")})
		if !errors.Is(err,ctx.Err()) { t.Fatal("context error",err) }
	}
}
