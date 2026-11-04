package sqltype

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestIntarraySetValues(t *testing.T) {
	types := testTypes()
	array, integer, boolean := types[1007], types[Int4], types[Bool]
	input := func(text string) Value {
		t.Helper()
		v, err := array.Input(t.Context(), text, -1)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	scalar, _ := Integer(integer, 3)
	for _, tc := range []struct {
		op                IntarraySetOperation
		left, right, want string
	}{
		{IntarrayOverlap, "{3,1,3,2}", "{2,4,2}", "t"},
		{IntarrayOverlap, "{}", "{}", "f"},
		{IntarrayContains, "{1}", "{1,1}", "t"},
		{IntarrayContained, "{1,1}", "{1}", "t"},
		{IntarrayContains, "{}", "{}", "t"},
		{IntarrayContained, "{}", "{1}", "t"},
		{IntarrayContains, "{}", "{1}", "f"},
		{IntarrayPushElem, "{{3,1},{3,2}}", "", "{3,1,3,2,3}"},
		{IntarrayPushArray, "{3,1,3,2}", "{2,4,2}", "{3,1,3,2,2,4,2}"},
		{IntarrayDeleteElem, "{3,1,3,2}", "", "{1,2}"},
		{IntarrayDeleteElem, "[0:3]={3,2,1,2}", "", "[0:2]={2,1,2}"},
		{IntarrayDeleteElem, "{{3,2},{1,2}}", "", "{{2},{1},{2}}"},
		{IntarrayDeleteElem, "{{{3,1},{3,2}}}", "", "{{{1}},{{2}}}"},
		{IntarrayDeleteElem, "{{{3,3}}}", "", "{}"},
		{IntarrayDeleteElem, "[0:1]={1,2}", "", "[0:1]={1,2}"},
		{IntarraySubtract, "{3,1,3,2}", "{2}", "{1,3}"},
		{IntarraySubtract, "{3,1,3,2}", "{}", "{1,2,3}"},
		{IntarrayUnionElem, "{3,1,3,2}", "", "{1,2,3}"},
		{IntarrayUnion, "{3,1,3,2}", "{2,4,2}", "{1,2,3,4}"},
		{IntarrayUnion, "{2147483647,-2147483648}", "{0,-2147483648}", "{-2147483648,0,2147483647}"},
		{IntarrayIntersect, "{3,1,3,2}", "{2,4,2}", "{2}"},
		{IntarrayIntersect, "{3}", "{2}", "{}"},
	} {
		left, right, result := input(tc.left), scalar, array
		if tc.right != "" {
			right = input(tc.right)
		}
		if tc.op <= IntarrayContained {
			result = boolean
		}
		leftElements, leftDimensions := slices.Clone(left.array.elements), slices.Clone(left.array.dimensions)
		var rightElements []Value
		var rightDimensions []ArrayDimension
		if right.array != nil {
			rightElements = slices.Clone(right.array.elements)
			rightDimensions = slices.Clone(right.array.dimensions)
		}
		value, err := tc.op.Eval(t.Context(), left, right, array, right.typ, result)
		if err != nil {
			t.Fatal(tc, err)
		}
		text, err := result.Output(t.Context(), value)
		if err != nil || text != tc.want {
			t.Fatal(tc, text, err)
		}
		if !reflect.DeepEqual(left.array.elements, leftElements) || !reflect.DeepEqual(left.array.dimensions, leftDimensions) || right.array != nil && (!reflect.DeepEqual(right.array.elements, rightElements) || !reflect.DeepEqual(right.array.dimensions, rightDimensions)) {
			t.Fatal("kernel mutated borrowed backing", tc)
		}
		if value.array != nil && value.array != left.array && value.array != right.array && value.ArrayLen() != 0 {
			old := value.array.elements[0]
			value.array.elements[0] = Null(integer)
			if !reflect.DeepEqual(left.array.elements, leftElements) || right.array != nil && !reflect.DeepEqual(right.array.elements, rightElements) {
				t.Fatal("output aliases operand elements", tc)
			}
			value.array.elements[0] = old
			if value.ArrayRank() != 0 {
				value.array.dimensions[0].Lower++
				if !reflect.DeepEqual(left.array.dimensions, leftDimensions) || right.array != nil && !reflect.DeepEqual(right.array.dimensions, rightDimensions) {
					t.Fatal("output aliases operand dimensions", tc)
				}
			}
		}
	}
	invalid, empty := input("{NULL}"), input("{}")
	for op := IntarrayOverlap; op < IntarraySetOperationCount; op++ {
		right, result := empty, array
		if op == IntarrayPushElem || op == IntarrayDeleteElem || op == IntarrayUnionElem {
			right = scalar
		}
		if op <= IntarrayContained {
			result = boolean
		}
		for _, pair := range [][2]Value{{invalid, right}, {Null(array), right}, {invalid, Null(right.typ)}} {
			value, err := op.Eval(t.Context(), pair[0], pair[1], array, right.typ, result)
			if pair[0].IsNull() || pair[1].IsNull() {
				if err != nil || !value.IsNull() || value.typ != result {
					t.Fatal("whole NULL must win", op, value, err)
				}
			} else {
				var diagnostic *sqlstate.Error
				if !errors.As(err, &diagnostic) || diagnostic.Code != "22004" || diagnostic.Message != "array must not contain nulls" {
					t.Fatal("NULL-element validation", op, err)
				}
			}
		}
		if right.typ == array {
			if _, err := op.Eval(t.Context(), empty, invalid, array, array, result); err == nil {
				t.Fatal("right NULL element escaped empty shortcut", op)
			}
		}
		if _, err := op.Eval(t.Context(), empty, right, array, right.typ, integer); err == nil {
			t.Fatal("wrong result admitted", op)
		}
		foreign := *array
		if _, err := op.Eval(t.Context(), Null(&foreign), right, array, right.typ, result); err == nil {
			t.Fatal("foreign input generation admitted", op)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := IntarrayUnion.Eval(ctx, empty, empty, array, array, array); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled kernel", err)
	}
}
