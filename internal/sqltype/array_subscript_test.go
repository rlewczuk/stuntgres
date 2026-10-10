package sqltype

import (
	"context"
	"errors"
	"math"
	"reflect"
	"testing"
)

func arraySubscriptInput(t *testing.T, d *Descriptor, text string) Value {
	t.Helper()
	v, err := d.Input(context.Background(), text, -1)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func arraySubscriptOutput(t *testing.T, v Value, want string) {
	t.Helper()
	got, err := v.Type().Output(context.Background(), v)
	if err != nil || got != want {
		t.Fatalf("output=%q/%v want %q", got, err, want)
	}
}
func arrayElementBound(index int32) ArraySubscriptValue {
	return ArraySubscriptValue{Upper: index, HasUpper: true}
}
func arraySliceBound(lower, upper int32) ArraySubscriptValue {
	return ArraySubscriptValue{Lower: lower, Upper: upper, HasLower: true, HasUpper: true}
}

func TestArraySubscriptOffsetsAndSlices(t *testing.T) {
	ctx := context.Background()
	d := testTypes()[1007]
	original := arraySubscriptInput(t, d, "[-1:0][3:4]={{5,NULL},{7,8}}")
	for _, tc := range []struct {
		bounds []ArraySubscriptValue
		want   int64
		null   bool
	}{
		{[]ArraySubscriptValue{arrayElementBound(-1), arrayElementBound(3)}, 5, false},
		{[]ArraySubscriptValue{arrayElementBound(0), arrayElementBound(4)}, 8, false},
		{[]ArraySubscriptValue{arrayElementBound(-1), arrayElementBound(4)}, 0, true},
		{[]ArraySubscriptValue{arrayElementBound(1), arrayElementBound(3)}, 0, true},
		{[]ArraySubscriptValue{arrayElementBound(-1)}, 0, true},
		{[]ArraySubscriptValue{{UpperNull: true, HasUpper: true}, arrayElementBound(3)}, 0, true},
	} {
		v, err := ArrayFetch(ctx, original, tc.bounds)
		if err != nil || v.Type() != d.Element() || v.IsNull() != tc.null {
			t.Fatal(v, err, tc)
		}
		if !tc.null {
			n, err := v.Integer()
			if err != nil || n != tc.want {
				t.Fatal(n, err, tc)
			}
		}
	}
	for _, tc := range []struct {
		bounds []ArraySubscriptValue
		want   string
	}{
		{[]ArraySubscriptValue{{}}, "{{5,NULL},{7,8}}"},
		{[]ArraySubscriptValue{arraySliceBound(-9, 0), arraySliceBound(1, 3)}, "{{5},{7}}"},
		{[]ArraySubscriptValue{arraySliceBound(9, 10)}, "{}"},
		{[]ArraySubscriptValue{arraySliceBound(0, -1)}, "{}"},
		{[]ArraySubscriptValue{arraySliceBound(-1, 0), arraySliceBound(3, 4), arraySliceBound(1, 1)}, "{}"},
	} {
		v, err := ArraySlice(ctx, original, tc.bounds)
		if err != nil {
			t.Fatal(err)
		}
		arraySubscriptOutput(t, v, tc.want)
		for _, dim := range v.ArrayDimensions() {
			if dim.Lower != 1 {
				t.Fatal("slice retained source bounds")
			}
		}
	}
	for _, base := range []Value{original, Null(d), arraySubscriptInput(t, d, "{}")} {
		v, err := ArraySlice(ctx, base, []ArraySubscriptValue{{HasLower: true, LowerNull: true}})
		if err != nil || !v.IsNull() || v.Type() != d {
			t.Fatal(v, err)
		}
	}
	arraySubscriptOutput(t, original, "[-1:0][3:4]={{5,NULL},{7,8}}")
}

func TestArraySubscriptImmutableAssignments(t *testing.T) {
	ctx := context.Background()
	d := testTypes()[1007]
	rhs, _ := Integer(d.Element(), 9)
	original := arraySubscriptInput(t, d, "{1,2,3}")
	for _, tc := range []struct {
		index int32
		want  string
	}{{5, "{1,2,3,NULL,9}"}, {-1, "[-1:3]={9,NULL,1,2,3}"}, {2, "{1,9,3}"}} {
		v, err := ArraySetElement(ctx, original, []ArraySubscriptValue{arrayElementBound(tc.index)}, rhs)
		if err != nil {
			t.Fatal(err)
		}
		arraySubscriptOutput(t, v, tc.want)
	}
	created, err := ArraySetElement(ctx, Null(d), []ArraySubscriptValue{arrayElementBound(2), arrayElementBound(4)}, Null(d.Element()))
	if err != nil {
		t.Fatal(err)
	}
	arraySubscriptOutput(t, created, "[2:2][4:4]={{NULL}}")
	matrix := arraySubscriptInput(t, d, "{{1,2},{3,4}}")
	updated, err := ArraySetSlice(ctx, matrix, []ArraySubscriptValue{arraySliceBound(1, 2), arraySliceBound(1, 1)}, arraySubscriptInput(t, d, "[0:2]={8,9,10}"))
	if err != nil {
		t.Fatal(err)
	}
	arraySubscriptOutput(t, updated, "{{8,2},{9,4}}")
	updated, err = ArraySetSlice(ctx, original, []ArraySubscriptValue{arraySliceBound(1, 2)}, arraySubscriptInput(t, d, "{{7,8},{9,10}}"))
	if err != nil {
		t.Fatal(err)
	}
	arraySubscriptOutput(t, updated, "{7,8,3}")
	updated, err = ArraySetSlice(ctx, Null(d), []ArraySubscriptValue{{}}, Null(d))
	if err != nil {
		t.Fatal(err)
	}
	arraySubscriptOutput(t, updated, "{}")
	same, err := ArraySetSlice(ctx, original, []ArraySubscriptValue{arraySliceBound(1, 2)}, Null(d))
	if err != nil || same.array != original.array {
		t.Fatal("NULL slice RHS copied/replaced immutable input", err)
	}
	_, err = ArraySetElement(ctx, matrix, []ArraySubscriptValue{arrayElementBound(3), arrayElementBound(1)}, rhs)
	arrayUnitDiagnostic(t, err, "2202E", "array subscript out of range", "")
	_, err = ArraySetElement(ctx, matrix, []ArraySubscriptValue{arrayElementBound(1)}, rhs)
	arrayUnitDiagnostic(t, err, "2202E", "wrong number of array subscripts", "")
	_, err = ArraySetElement(ctx, original, []ArraySubscriptValue{{UpperNull: true, HasUpper: true}}, rhs)
	arrayUnitDiagnostic(t, err, "22004", "array subscript in assignment must not be null", "")
	_, err = ArraySetSlice(ctx, original, []ArraySubscriptValue{arraySliceBound(1, 2)}, arraySubscriptInput(t, d, "{7}"))
	arrayUnitDiagnostic(t, err, "2202E", "source array too small", "")
	_, err = ArraySetSlice(ctx, Null(d), []ArraySubscriptValue{{Upper: 2, HasUpper: true}}, arraySubscriptInput(t, d, "{7,8}"))
	arrayUnitDiagnostic(t, err, "2202E", "array slice subscript must provide both boundaries", "When assigning to a slice of an empty array value, slice boundaries must be fully specified.")
	arraySubscriptOutput(t, original, "{1,2,3}")
	arraySubscriptOutput(t, matrix, "{{1,2},{3,4}}")
}

func TestArrayConstructionShapeAndLimits(t *testing.T) {
	ctx := context.Background()
	d := testTypes()[1007]
	child := arraySubscriptInput(t, d, "[0:1]={1,2}")
	v, err := ConstructArray(ctx, d, []Value{child, child}, true)
	if err != nil {
		t.Fatal(err)
	}
	arraySubscriptOutput(t, v, "[1:2][0:1]={{1,2},{1,2}}")
	for _, other := range []Value{Null(d), arraySubscriptInput(t, d, "{}"), arraySubscriptInput(t, d, "{1,2}"), arraySubscriptInput(t, d, "{1}")} {
		_, err := ConstructArray(ctx, d, []Value{child, other}, true)
		arrayUnitDiagnostic(t, err, "2202E", "multidimensional arrays must have array expressions with matching dimensions", "")
	}
	v, err = ConstructArray(ctx, d, []Value{Null(d), arraySubscriptInput(t, d, "{}")}, true)
	if err != nil || len(v.ArrayDimensions()) != 0 {
		t.Fatal(v, err)
	}
	_, err = arrayShapeSize([]ArrayDimension{{Lower: math.MaxInt32, Length: 1}})
	arrayUnitDiagnostic(t, err, "54000", "array lower bound is too large: 2147483647", "")
	_, err = arrayShapeSize([]ArrayDimension{{1, math.MaxInt32 - 1}, {1, 2}})
	arrayUnitDiagnostic(t, err, "54000", "array size exceeds the maximum allowed (134217727)", "")
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := ArrayFetch(canceled, child, []ArraySubscriptValue{arrayElementBound(0)}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := ArraySetElement(canceled, child, []ArraySubscriptValue{arrayElementBound(0)}, Null(d.Element())); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := ConstructArray(canceled, d, nil, false); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	arraySubscriptOutput(t, child, "[0:1]={1,2}")
}

func TestArrayElementwiseCoercionContexts(t *testing.T) {
	types := testTypes()
	ctx := context.Background()
	from, to := types[1007], types[1016]
	input := arraySubscriptInput(t, from, "[0:2]={1,NULL,2}")
	for _, mode := range []CoercionContext{Implicit, Assignment, Explicit} {
		plan, err := PlanCoercion(from.Usage(), to.Usage(), mode)
		if err != nil {
			t.Fatal(err)
		}
		value, err := plan.Apply(ctx, input, InputEnvironment{}, OutputEnvironment{})
		if err != nil {
			t.Fatal(err)
		}
		if value.Type() != to || value.ArrayElement(1).Type() != to.Element() || !value.ArrayElement(1).IsNull() || !reflect.DeepEqual(input.ArrayDimensions(), value.ArrayDimensions()) {
			t.Fatal("array descriptor/bounds lost")
		}
		arraySubscriptOutput(t, value, "[0:2]={1,NULL,2}")
	}
	if _, err := PlanCoercion(to.Usage(), from.Usage(), Implicit); err == nil {
		t.Fatal("implicit narrowing admitted")
	}
	for _, mode := range []CoercionContext{Assignment, Explicit} {
		if _, err := PlanCoercion(to.Usage(), from.Usage(), mode); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := PlanCoercion(types[1009].Usage(), from.Usage(), Assignment); err == nil {
		t.Fatal("TEXT[] assignment became input coercion")
	}
	identity, err := PlanCoercion(from.Usage(), from.Usage(), Implicit)
	if err != nil {
		t.Fatal(err)
	}
	same, err := identity.Apply(ctx, input, InputEnvironment{}, OutputEnvironment{})
	if err != nil || same.array != input.array {
		t.Fatal("identity allocated/copies", err)
	}
	first, _ := PlanCoercion(from.Usage(), to.Usage(), Implicit)
	second, _ := PlanCoercion(from.Usage(), to.Usage(), Implicit)
	if !first.Equal(second) {
		t.Fatal("equivalent retained child coercions differ")
	}
}

func TestArraySubscriptSliceOwnershipAndGaps(t *testing.T) {
	ctx := context.Background()
	d := testTypes()[1007]
	original := arraySubscriptInput(t, d, "[0:2]={1,NULL,3}")
	for _, tc := range []struct {
		lower, upper int32
		rhs, want    string
	}{
		{-3, -2, "{7,8}", "[-3:2]={7,8,NULL,1,NULL,3}"},
		{5, 6, "{7,8}", "[0:6]={1,NULL,3,NULL,NULL,7,8}"},
		{-1, 0, "{7,8}", "[-1:2]={7,8,NULL,3}"},
		{-1, 3, "{7,8,9,10,11}", "[-1:3]={7,8,9,10,11}"},
		{0, 1, "{7,8}", "[0:2]={7,8,3}"},
	} {
		rhs := arraySubscriptInput(t, d, tc.rhs)
		value, err := ArraySetSlice(ctx, original, []ArraySubscriptValue{arraySliceBound(tc.lower, tc.upper)}, rhs)
		if err != nil {
			t.Fatal(err)
		}
		arraySubscriptOutput(t, value, tc.want)
		for _, cell := range value.array.elements {
			if cell.Type() != d.Element() {
				t.Fatal("uninitialized gap or wrong generation")
			}
		}
		if value.array == original.array || &value.array.elements[0] == &original.array.elements[0] || &value.array.elements[0] == &rhs.array.elements[0] {
			t.Fatal("mutation aliases source elements")
		}
		if tc.lower == 0 && tc.upper == 1 && &value.array.dimensions[0] != &original.array.dimensions[0] {
			t.Fatal("no-enlargement unnecessarily replaced immutable dimensions")
		}
		arraySubscriptOutput(t, original, "[0:2]={1,NULL,3}")
		arraySubscriptOutput(t, rhs, tc.rhs)
	}
	matrix := arraySubscriptInput(t, d, "[-1:0][3:4]={{1,NULL},{3,4}}")
	rhs := arraySubscriptInput(t, d, "{7,8}")
	value, err := ArraySetSlice(ctx, matrix, []ArraySubscriptValue{arraySliceBound(-1, 0), arraySliceBound(4, 4)}, rhs)
	if err != nil {
		t.Fatal(err)
	}
	arraySubscriptOutput(t, value, "[-1:0][3:4]={{1,7},{3,8}}")
	if &value.array.dimensions[0] != &matrix.array.dimensions[0] || value.array == matrix.array {
		t.Fatal("no-enlargement ownership changed")
	}
	arraySubscriptOutput(t, matrix, "[-1:0][3:4]={{1,NULL},{3,4}}")
	arraySubscriptOutput(t, rhs, "{7,8}")
	for _, base := range []Value{Null(d), arraySubscriptInput(t, d, "{}")} {
		source := arraySubscriptInput(t, d, "[0:2]={7,NULL,9}")
		value, err := ArraySetSlice(ctx, base, []ArraySubscriptValue{arraySliceBound(2, 3)}, source)
		if err != nil {
			t.Fatal(err)
		}
		arraySubscriptOutput(t, value, "[2:3]={7,NULL}")
		if value.ArrayElement(1).Type() != d.Element() || &value.array.elements[0] == &source.array.elements[0] {
			t.Fatal("empty replacement did not own exact typed cells")
		}
		arraySubscriptOutput(t, source, "[0:2]={7,NULL,9}")
		if !base.IsNull() {
			arraySubscriptOutput(t, base, "{}")
		}
	}
	element, _ := Integer(d.Element(), 7)
	for _, tc := range []struct {
		index int32
		want  string
	}{{-3, "[-3:2]={7,NULL,NULL,1,NULL,3}"}, {5, "[0:5]={1,NULL,3,NULL,NULL,7}"}} {
		value, err := ArraySetElement(ctx, original, []ArraySubscriptValue{arrayElementBound(tc.index)}, element)
		if err != nil {
			t.Fatal(err)
		}
		arraySubscriptOutput(t, value, tc.want)
		for _, cell := range value.array.elements {
			if cell.Type() != d.Element() {
				t.Fatal("element enlargement left untyped gap")
			}
		}
		arraySubscriptOutput(t, original, "[0:2]={1,NULL,3}")
	}
}

func TestArraySubscriptElementEnlargementOverflow(t *testing.T) {
	d := testTypes()[1007]
	original := arraySubscriptInput(t, d, "{10,NULL,30}")
	element, _ := Integer(d.Element(), 7)
	_, err := ArraySetElement(context.Background(), original, []ArraySubscriptValue{arrayElementBound(math.MinInt32)}, element)
	arrayUnitDiagnostic(t, err, "54000", "array size exceeds the maximum allowed (134217727)", "")
	arraySubscriptOutput(t, original, "{10,NULL,30}")
}

func TestArraySubscriptSliceValidationPrecedence(t *testing.T) {
	ctx := context.Background()
	d := testTypes()[1007]
	matrix := arraySubscriptInput(t, d, "{{1,2},{3,4}}")
	one := arraySubscriptInput(t, d, "{7}")
	for _, tc := range []struct {
		bounds  []ArraySubscriptValue
		message string
	}{
		{[]ArraySubscriptValue{arraySliceBound(1, 3), arraySliceBound(1, 1)}, "array subscript out of range"},
		{[]ArraySubscriptValue{arraySliceBound(1, 3), arraySliceBound(2, 1)}, "array subscript out of range"},
		{[]ArraySubscriptValue{arraySliceBound(2, 1), arraySliceBound(1, 3)}, "upper bound cannot be less than lower bound"},
	} {
		_, err := ArraySetSlice(ctx, matrix, tc.bounds, one)
		arrayUnitDiagnostic(t, err, "2202E", tc.message, "")
	}
	_, err := ArraySetSlice(ctx, arraySubscriptInput(t, d, "{1,2,3}"), []ArraySubscriptValue{arraySliceBound(1, math.MaxInt32)}, one)
	arrayUnitDiagnostic(t, err, "54000", "array size exceeds the maximum allowed (134217727)", "")
	for _, base := range []Value{Null(d), arraySubscriptInput(t, d, "{}")} {
		for _, bounds := range [][]ArraySubscriptValue{{arraySliceBound(2, 1)}, {arraySliceBound(1, 1), arraySliceBound(2, 1)}} {
			value, err := ArraySetSlice(ctx, base, bounds, one)
			if err != nil {
				t.Fatal(err)
			}
			arraySubscriptOutput(t, value, "{}")
			if len(value.array.dimensions) != 0 || len(value.array.elements) != 0 {
				t.Fatal("zero span not canonical")
			}
		}
		for _, bounds := range [][]ArraySubscriptValue{{arraySliceBound(3, 1)}, {arraySliceBound(2, 1), arraySliceBound(3, 1)}, {arraySliceBound(math.MinInt32, math.MaxInt32)}} {
			_, err := ArraySetSlice(ctx, base, bounds, one)
			arrayUnitDiagnostic(t, err, "54000", "array size exceeds the maximum allowed (134217727)", "")
		}
		_, err := ArraySetSlice(ctx, base, []ArraySubscriptValue{arraySliceBound(math.MaxInt32, math.MaxInt32)}, one)
		arrayUnitDiagnostic(t, err, "54000", "array lower bound is too large: 2147483647", "")
		value, err := ArraySetSlice(ctx, base, []ArraySubscriptValue{arraySliceBound(3, 1)}, Null(d))
		if err != nil {
			t.Fatal(err)
		}
		arraySubscriptOutput(t, value, "{}")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := ArraySetSlice(canceled, matrix, []ArraySubscriptValue{arraySliceBound(1, 1)}, one); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	arraySubscriptOutput(t, matrix, "{{1,2},{3,4}}")
}

func TestArraySubscriptEmptySliceBoundsOrder(t *testing.T) {
	ctx := context.Background()
	d := testTypes()[1007]
	one := arraySubscriptInput(t, d, "{7}")
	maximum := arraySliceBound(math.MaxInt32, math.MaxInt32)
	zero := arraySliceBound(2, 1)
	for _, bounds := range [][]ArraySubscriptValue{{maximum, zero}, {zero, maximum}} {
		_, err := ArraySetSlice(ctx, Null(d), bounds, one)
		arrayUnitDiagnostic(t, err, "54000", "array lower bound is too large: 2147483647", "")
	}
	_, err := ArraySetSlice(ctx, Null(d), []ArraySubscriptValue{maximum}, arraySubscriptInput(t, d, "{}"))
	arrayUnitDiagnostic(t, err, "2202E", "source array too small", "")
	value, err := ArraySetSlice(ctx, Null(d), []ArraySubscriptValue{arraySliceBound(math.MaxInt32, math.MaxInt32-1), arraySliceBound(1, 1)}, one)
	if err != nil {
		t.Fatal(err)
	}
	arraySubscriptOutput(t, value, "{}")
}
