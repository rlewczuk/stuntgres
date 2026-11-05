package sqltype

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestIntarraySliceValues(t *testing.T) {
	types := testTypes()
	array, integer := types[1007], types[Int4]
	input := func(text string) Value {
		t.Helper()
		v, err := array.Input(t.Context(), text, -1)
		if err != nil { t.Fatal(err) }
		return v
	}
	number := func(n int64) Value { v, err := Integer(integer, n); if err != nil { t.Fatal(err) }; return v }
	const a = "{11,22,33,22}"
	for _,tc := range []struct { op IntarraySliceOperation; array string; start, length int64; want string }{
		{IntarrayIcount,a,0,0,"4"},{IntarrayIcount,"{}",0,0,"0"},{IntarrayIcount,"{NULL,1,NULL}",0,0,"3"},
		{IntarrayIcount,"{{11,22},{33,22}}",0,0,"4"},
		{IntarrayIdx,a,22,0,"2"},{IntarrayIdx,a,99,0,"0"},{IntarrayIdx,"{}",22,0,"0"},
		{IntarrayIdx,"[0:3]={11,22,33,22}",22,0,"2"},{IntarrayIdx,"{{11,22},{33,22}}",33,0,"3"},
		{IntarrayIdx,"{-2147483648,2147483647,-2147483648}",-2147483648,0,"1"},
		{IntarrayIdx,"{-2147483648,2147483647}",2147483647,0,"2"},
		{IntarraySubarray,a,2,0,"{22,33,22}"},{IntarraySubarray,a,0,0,a},{IntarraySubarray,a,1,0,a},
		{IntarraySubarray,a,-1,0,"{22}"},{IntarraySubarray,a,-4,0,a},{IntarraySubarray,a,-5,0,a},
		{IntarraySubarray,a,5,0,"{}"},{IntarraySubarray,a,-2147483648,0,a},{IntarraySubarray,a,2147483647,0,"{}"},
		{IntarraySubarrayLength,a,2,3,"{22,33,22}"},{IntarraySubarrayLength,a,2,0,"{22,33,22}"},
		{IntarraySubarrayLength,a,2,2,"{22,33}"},{IntarraySubarrayLength,a,-5,2,"{11}"},
		{IntarraySubarrayLength,a,-6,2,"{}"},{IntarraySubarrayLength,a,2,-1,"{22,33}"},
		{IntarraySubarrayLength,a,0,-1,"{11,22,33}"},{IntarraySubarrayLength,a,2,-3,"{}"},
		{IntarraySubarrayLength,a,3,100,"{33,22}"},{IntarraySubarrayLength,a,1,-2147483648,"{}"},
		{IntarraySubarrayLength,a,2,2147483647,"{}"},{IntarraySubarrayLength,a,2147483647,2147483647,"{}"},
		{IntarraySubarrayLength,a,1,2147483647,a},{IntarraySubarray,"{}",1,0,"{}"},
		{IntarraySubarrayLength,"{}",1,2,"{}"},
		{IntarraySubarray,"[0:3]={11,22,33,22}",2,0,"{22,33,22}"},
		{IntarraySubarrayLength,"{{11,22},{33,22}}",2,2,"{22,33}"},
		{IntarrayIntset,"{}",-2147483648,0,"{-2147483648}"},{IntarrayIntset,"{}",2147483647,0,"{2147483647}"},
	} {
		left := input(tc.array)
		args := [3]Value{left,number(tc.start),number(tc.length)}
		formals := [3]*Descriptor{array,integer,integer}
		result := array
		if tc.op == IntarrayIcount || tc.op == IntarrayIdx { result = integer }
		if tc.op == IntarrayIntset { args[0],formals[0] = number(tc.start),integer }
		elements, dimensions := slices.Clone(left.array.elements),slices.Clone(left.array.dimensions)
		value,err := tc.op.Eval(t.Context(),args[:tc.op.Arity()],formals[:tc.op.Arity()],result)
		if err != nil { t.Fatal(tc,err) }
		text,err := result.Output(t.Context(),value)
		if err != nil || text != tc.want || value.Type() != result { t.Fatal(tc,text,err) }
		if !reflect.DeepEqual(left.array.elements,elements) || !reflect.DeepEqual(left.array.dimensions,dimensions) { t.Fatal("borrowed input mutated",tc) }
		if value.array != nil {
			if value.ArrayLen() == 0 {
				if value.ArrayRank() != 0 { t.Fatal("noncanonical empty shape") }
			} else {
				if value.ArrayRank() != 1 || value.ArrayDimension(0) != (ArrayDimension{Lower:1,Length:int32(value.ArrayLen())}) || value.array == left.array || left.ArrayLen() != 0 && &value.array.elements[0] == &left.array.elements[0] { t.Fatal("result not independently owned and flat",tc) }
				value.array.elements[0] = Null(integer)
				value.array.dimensions[0].Lower = 99
				if !reflect.DeepEqual(left.array.elements,elements) || !reflect.DeepEqual(left.array.dimensions,dimensions) { t.Fatal("result aliases source",tc) }
			}
		}
	}
	for op := IntarrayIcount; op < IntarraySliceOperationCount; op++ {
		formals := [3]*Descriptor{array,integer,integer}
		args := [3]Value{input("{22,NULL}"),number(99),number(-99)}
		result := array
		if op == IntarrayIcount || op == IntarrayIdx { result = integer }
		if op == IntarrayIntset { formals[0],args[0] = integer,number(7) }
		n := op.Arity()
		for i := range n {
			nulls := args; nulls[i] = Null(formals[i])
			value,err := op.Eval(t.Context(),nulls[:n],formals[:n],result)
			if err != nil || !value.IsNull() || value.Type() != result { t.Fatal("strict typed NULL",op,i,err) }
			foreign := *formals[i]
			nulls[i] = Null(&foreign)
			if _,err := op.Eval(t.Context(),nulls[:n],formals[:n],result); err == nil { t.Fatal("foreign NULL generation",op,i) }
		}
		if op == IntarrayIdx || op == IntarraySubarray || op == IntarraySubarrayLength {
			for _,start := range []int64{22,99} {
				args[1] = number(start)
				_,err := op.Eval(t.Context(),args[:n],formals[:n],result)
				var diagnostic *sqlstate.Error
				if !errors.As(err,&diagnostic) || diagnostic.Code != "22004" || diagnostic.Message != "array must not contain nulls" { t.Fatal("validation bypassed match/boundary",op,err) }
			}
		}
		foreign := *result
		if op == IntarrayIntset { foreign.element = new(*integer) }
		if _,err := op.Eval(t.Context(),args[:n],formals[:n],&foreign); err == nil { t.Fatal("foreign result generation",op) }
		if _,err := op.Eval(t.Context(),args[:n-1],formals[:n],result); err == nil { t.Fatal("wrong argument arity",op) }
	}
}

type intarraySliceCancelContext struct { context.Context; remaining int }
func (c *intarraySliceCancelContext) Err() error {
	c.remaining--
	if c.remaining <= 0 { return context.Canceled }
	return c.Context.Err()
}

func TestIntarraySliceCancellation(t *testing.T) {
	types := testTypes()
	array,integer := types[1007],types[Int4]
	left,err := array.Input(t.Context(),"{11,22,33,22}",-1); if err != nil { t.Fatal(err) }
	one,_ := Integer(integer,1)
	zero,_ := Integer(integer,0)
	missing,_ := Integer(integer,99)
	elements,dimensions := slices.Clone(left.array.elements),slices.Clone(left.array.dimensions)
	for op := IntarrayIcount; op < IntarraySliceOperationCount; op++ {
		args := [3]Value{left,one,zero}
		formals := [3]*Descriptor{array,integer,integer}
		result := array
		if op == IntarrayIcount || op == IntarrayIdx { result = integer }
		if op == IntarrayIdx { args[1] = missing }
		if op == IntarrayIntset { args[0],formals[0] = one,integer }
		polls := []int{1}
		if op == IntarrayIdx || op == IntarraySubarray || op == IntarraySubarrayLength {
			// Entry is poll1, full validation polls2..5. Poll7 interrupts
			// searching/copying after one element has already been processed.
			polls = []int{1,3,7}
		}
		for _,at := range polls {
			ctx := &intarraySliceCancelContext{Context:t.Context(),remaining:at}
			value,err := op.Eval(ctx,args[:op.Arity()],formals[:op.Arity()],result)
			if !errors.Is(err,context.Canceled) || value != (Value{}) { t.Fatal("canceled kernel returned a value",op,at,err) }
			if !reflect.DeepEqual(left.array.elements,elements) || !reflect.DeepEqual(left.array.dimensions,dimensions) { t.Fatal("canceled kernel mutated source") }
		}
		if op != IntarrayIntset {
			empty,err := array.Input(t.Context(),"{}",-1)
			if err != nil {t.Fatal(err)}
			args[0] = empty
			ctx,cancel := context.WithCancel(t.Context())
			cancel()
			if value,err := op.Eval(ctx,args[:op.Arity()],formals[:op.Arity()],result); !errors.Is(err,context.Canceled) || value != (Value{}) {t.Fatal("empty shortcut ignored context",op,err)}
		}
	}
}
