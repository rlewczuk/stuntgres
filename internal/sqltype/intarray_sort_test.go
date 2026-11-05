package sqltype

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestIntarraySortValues(t *testing.T) {
	types := testTypes()
	array, text := types[1007], types[Text]
	input := func(d *Descriptor, s string) Value {
		t.Helper()
		v, err := d.Input(t.Context(), s, -1)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	for _, tc := range []struct{ source, asc, desc, uniq string }{
		{"{}", "{}", "{}", "{}"}, {"{7}", "{7}", "{7}", "{7}"},
		{"{3,1,1,2}", "{1,1,2,3}", "{3,2,1,1}", "{3,1,2}"},
		{"{1,2,2,3,1,1}", "{1,1,1,2,2,3}", "{3,2,2,1,1,1}", "{1,2,3,1}"},
		{"{2,1,2}", "{1,2,2}", "{2,2,1}", "{2,1,2}"},
		{"{1,2,3}", "{1,2,3}", "{3,2,1}", "{1,2,3}"},
		{"{3,2,1}", "{1,2,3}", "{3,2,1}", "{3,2,1}"},
		{"{2,2,2}", "{2,2,2}", "{2,2,2}", "{2}"},
		{"{2147483647,-2147483648,0,-1}", "{-2147483648,-1,0,2147483647}", "{2147483647,0,-1,-2147483648}", "{2147483647,-2147483648,0,-1}"},
		{"[0:2]={3,1,2}", "[0:2]={1,2,3}", "[0:2]={3,2,1}", "[0:2]={3,1,2}"},
		{"{{4,1},{3,2}}", "{{1,2},{3,4}}", "{{4,3},{2,1}}", "{{4,1},{3,2}}"},
		{"{{1,1},{2,2}}", "{{1,1},{2,2}}", "{{2,2},{1,1}}", "{{1},{2}}"},
		{"[0:1][-2:-1]={{1,1},{2,2}}", "[0:1][-2:-1]={{1,1},{2,2}}", "[0:1][-2:-1]={{2,2},{1,1}}", "[0:1][-2:-2]={{1},{2}}"},
		{"[0:3]={1,1,2,2}", "[0:3]={1,1,2,2}", "[0:3]={2,2,1,1}", "[0:1]={1,2}"},
		{"{{1,2},{2,3}}", "{{1,2},{2,3}}", "{{3,2},{2,1}}", "{{1},{2},{3}}"},
	} {
		for op := IntarraySort; op < IntarraySortOperationCount; op++ {
			original := input(array, tc.source)
			externalElements, externalDimensions := slices.Clone(original.array.elements), slices.Clone(original.array.dimensions)
			left, err := ArrayValue(array, externalDimensions, externalElements)
			if err != nil {
				t.Fatal(err)
			}
			if len(externalElements) > 0 {
				externalElements[0] = Null(array.Element())
				externalDimensions[0].Lower = 99
			}
			if !reflect.DeepEqual(left.array, original.array) {
				t.Fatal("ArrayValue borrowed caller buffers")
			}
			args := [2]Value{left, input(text, "dEsC")}
			formals := [2]*Descriptor{array, text}
			want := tc.asc
			if op == IntarraySortDesc || op == IntarraySortDirection {
				want = tc.desc
			}
			if op == IntarrayUniq {
				want = tc.uniq
			}
			value, err := op.Eval(t.Context(), args[:op.Arity()], formals[:op.Arity()], array)
			if err != nil {
				t.Fatal(op, tc, err)
			}
			got, err := array.Output(t.Context(), value)
			if err != nil || got != want || value.Type() != array {
				t.Fatal(op, tc, got, err)
			}
			if !reflect.DeepEqual(left.array, original.array) {
				t.Fatal("kernel mutated borrowed input")
			}
			unchanged := left.ArrayLen() < 2 || op == IntarrayUniq && tc.source == tc.uniq
			if unchanged {
				if value != left {
					t.Fatal("no-op lost immutable input identity")
				}
			} else {
				if value.array == left.array || &value.array.elements[0] == &left.array.elements[0] || &value.array.dimensions[0] == &left.array.dimensions[0] {
					t.Fatal("changed result aliases borrowed buffers")
				}
				value.array.elements[0] = Null(array.Element())
				value.array.dimensions[0].Lower = 99
				if !reflect.DeepEqual(left.array, original.array) {
					t.Fatal("result mutation reached input")
				}
			}
		}
	}
	for op := IntarraySort; op < IntarraySortOperationCount; op++ {
		formals := [2]*Descriptor{array, text}
		args := [2]Value{input(array, "{1,NULL}"), input(text, "invalid")}
		n := op.Arity()
		for i := range n {
			nulls := args
			nulls[i] = Null(formals[i])
			v, err := op.Eval(t.Context(), nulls[:n], formals[:n], array)
			if err != nil || !v.IsNull() || v.Type() != array {
				t.Fatal("strict typed NULL", op, i, err)
			}
			foreign := *formals[i]
			nulls[i] = Null(&foreign)
			if _, err := op.Eval(t.Context(), nulls[:n], formals[:n], array); err == nil {
				t.Fatal("foreign NULL generation")
			}
		}
		for _, source := range []string{"{NULL}", "{1,NULL}"} {
			args[0] = input(array, source)
			_, err := op.Eval(t.Context(), args[:n], formals[:n], array)
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != "22004" || diagnostic.Message != "array must not contain nulls" {
				t.Fatal("NULL precedence", op, err)
			}
		}
		args[0] = input(array, "{2,1}")
		foreign := *array
		if _, err := op.Eval(t.Context(), args[:n], formals[:n], &foreign); err == nil {
			t.Fatal("foreign result admitted")
		}
		badFormals := formals
		badFormals[0] = &foreign
		if _, err := op.Eval(t.Context(), args[:n], badFormals[:n], &foreign); err == nil {
			t.Fatal("foreign operand/formal generation admitted")
		}
		badElement := *array.Element()
		broken := args[0]
		payload := *broken.array
		payload.elements = slices.Clone(payload.elements)
		payload.elements[0].typ = &badElement
		broken.array = &payload
		badArgs := args
		badArgs[0] = broken
		if _, err := op.Eval(t.Context(), badArgs[:n], formals[:n], array); err == nil {
			t.Fatal("foreign element generation admitted")
		}
		if _, err := op.Eval(t.Context(), args[:n-1], formals[:n], array); err == nil {
			t.Fatal("wrong arity admitted")
		}
		badFormals = formals
		badFormals[0] = text
		if err := op.Validate(badFormals[:n], array); err == nil {
			t.Fatal("wrong array formal admitted")
		}
	}
	for _, direction := range []string{"asc", "ASC", "aSc", "desc", "DESC", "dEsC", "", " asc", "asc ", "desc\n", "ascending", "descending", "ＡＳＣ", "deſc"} {
		args := []Value{input(array, "{2,1}"), input(text, direction)}
		v, err := IntarraySortDirection.Eval(t.Context(), args, []*Descriptor{array, text}, array)
		valid := direction == "asc" || direction == "ASC" || direction == "aSc" || direction == "desc" || direction == "DESC" || direction == "dEsC"
		if valid {
			want := "{1,2}"
			if direction == "desc" || direction == "DESC" || direction == "dEsC" {
				want = "{2,1}"
			}
			got, outputErr := array.Output(t.Context(), v)
			if err != nil || outputErr != nil || got != want {
				t.Fatal(direction, got, err, outputErr)
			}
		} else {
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != "22023" || diagnostic.Message != `second parameter must be "ASC" or "DESC"` {
				t.Fatal(direction, err)
			}
		}
		for _, short := range []string{"{}", "{7}"} {
			args[0] = input(array, short)
			v, err := IntarraySortDirection.Eval(t.Context(), args, []*Descriptor{array, text}, array)
			if err != nil || v != args[0] {
				t.Fatal("short direction bypass", direction, err)
			}
		}
	}
	t.Run("cancellation", func(t *testing.T) {
		left := input(array, "{4,4,3,3,2,2,1,1}")
		beforeElements, beforeDimensions := slices.Clone(left.array.elements), slices.Clone(left.array.dimensions)
		for op := IntarraySort; op < IntarraySortOperationCount; op++ {
			args := []Value{left, input(text, "ASC")}
			formals := []*Descriptor{array, text}
			// Entry1, validation2..9, sort-copy10..17 then comparator;
			// uniq count10..16 then compaction. No sleeps or scheduling races.
			for _, at := range []int{1, 4, 12, 20} {
				ctx := &intarraySliceCancelContext{Context: t.Context(), remaining: at}
				v, err := op.Eval(ctx, args[:op.Arity()], formals[:op.Arity()], array)
				if !errors.Is(err, context.Canceled) || v != (Value{}) {
					t.Fatal("canceled kernel returned result", op, at, err)
				}
				if !reflect.DeepEqual(left.array.elements, beforeElements) || !reflect.DeepEqual(left.array.dimensions, beforeDimensions) {
					t.Fatal("cancellation mutated input")
				}
			}
		}
	})
}
