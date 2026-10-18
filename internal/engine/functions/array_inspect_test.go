package functions

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

func arrayInspectTestCall(t *testing.T, kind ArrayInspectKind, input string, dimension int64) ArrayInspect {
	t.Helper()
	types := arraySearchTestTypes()
	a, err := types[1007].Input(context.Background(), input, -1)
	if err != nil {
		t.Fatal(err)
	}
	args := []expr.Expr{arraySearchTestLiteral(a)}
	if kind.Arity() == 2 {
		d, err := sqltype.Integer(types[sqltype.Int4], dimension)
		if err != nil {
			t.Fatal(err)
		}
		args = append(args, arraySearchTestLiteral(d))
	}
	call := ArrayInspect{Info: expr.Info{Usage: types[kind.Result()].Usage()}, Kind: kind, Args: args}
	if err := call.Validate(); err != nil {
		t.Fatal(err)
	}
	return call
}

func TestArrayInspectValues(t *testing.T) {
	ctx := &arraySearchTestContext{Context: context.Background()}
	for _, tc := range []struct {
		kind ArrayInspectKind
		d    int64
		want any
	}{
		{ArrayDims, 1, "[-2:-1][3:5]"}, {ArrayNDims, 1, int64(2)}, {ArrayCardinality, 1, int64(6)},
		{ArrayLength, 1, int64(2)}, {ArrayLower, 1, int64(-2)}, {ArrayUpper, 1, int64(-1)},
		{ArrayLength, 2, int64(3)}, {ArrayLower, 2, int64(3)}, {ArrayUpper, 2, int64(5)},
		{ArrayLength, 0, nil}, {ArrayLower, -1, nil}, {ArrayUpper, 3, nil}, {ArrayLength, 2147483647, nil}, {ArrayUpper, -2147483648, nil},
	} {
		call := arrayInspectTestCall(t, tc.kind, "[-2:-1][3:5]={{1,NULL,3},{4,5,6}}", tc.d)
		value, err := call.Eval(ctx)
		if err != nil || value.Type() != call.Usage.Type {
			t.Fatal(value, err)
		}
		var got any
		if !value.IsNull() {
			if tc.kind == ArrayDims {
				got, err = value.Text()
			} else {
				got, err = value.Integer()
			}
		}
		if err != nil || got != tc.want {
			t.Fatalf("%s dimension%d=%v want%v: %v", tc.kind.Name(), tc.d, got, tc.want, err)
		}
	}
	for kind := ArrayDims; kind <= ArrayCardinality; kind++ {
		call := arrayInspectTestCall(t, kind, "{}", 1)
		v, err := call.Eval(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if kind == ArrayCardinality {
			n, _ := v.Integer()
			if n != 0 || v.IsNull() {
				t.Fatal(v)
			}
		} else if !v.IsNull() {
			t.Fatal(v)
		}
		call.Args[0] = arraySearchTestLiteral(sqltype.Null(call.Args[0].ResultUsage().Type))
		v, err = call.Eval(ctx)
		if err != nil || !v.IsNull() || v.Type() != call.Usage.Type {
			t.Fatal(v, err)
		}
	}
}

func TestArrayInspectDemand(t *testing.T) {
	call := arrayInspectTestCall(t, ArrayLength, "{1}", 1)
	var order []int
	first, second := errors.New("first"), errors.New("second")
	a := arraySearchTestArgument{Info: expr.Info{Usage: call.Args[0].ResultUsage()}, value: sqltype.Null(call.Args[0].ResultUsage().Type), order: &order, index: 0}
	d := arraySearchTestArgument{Info: expr.Info{Usage: call.Args[1].ResultUsage()}, order: &order, index: 1, err: second}
	call.Args = []expr.Expr{a, d}
	ctx := &arraySearchTestContext{Context: context.Background()}
	if _, err := call.Eval(ctx); !errors.Is(err, second) || !reflect.DeepEqual(order, []int{0, 1}) {
		t.Fatal(order, err)
	}
	order = nil
	a.err = first
	call.Args[0] = a
	if _, err := call.Eval(ctx); !errors.Is(err, first) || !reflect.DeepEqual(order, []int{0}) {
		t.Fatal(order, err)
	}
	order = nil
	a.err, d.err = nil, nil
	d.value = sqltype.Null(d.Usage.Type)
	call.Args = []expr.Expr{a, d}
	v, err := call.Eval(ctx)
	if err != nil || !v.IsNull() || !reflect.DeepEqual(order, []int{0, 1}) {
		t.Fatal(v, order, err)
	}
	c, cancel := context.WithCancel(context.Background())
	cancel()
	order = nil
	if _, err := call.Eval(&arraySearchTestContext{Context: c}); !errors.Is(err, context.Canceled) || len(order) != 0 {
		t.Fatal(order, err)
	}
}

func TestArrayInspectNoCopy(t *testing.T) {
	ctx := &arraySearchTestContext{Context: context.Background()}
	for _, kind := range []ArrayInspectKind{ArrayLength, ArrayLower, ArrayUpper, ArrayNDims, ArrayCardinality} {
		call := arrayInspectTestCall(t, kind, "[-2:-1][3:5]={{1,NULL,3},{4,5,6}}", 2)
		input, _ := call.Args[0].Eval(ctx)
		if allocations := testing.AllocsPerRun(100, func() {
			if _, err := call.Eval(ctx); err != nil {
				panic(err)
			}
		}); allocations != 0 {
			t.Fatalf("%s allocations=%v", kind.Name(), allocations)
		}
		after, _ := call.Args[0].Eval(ctx)
		if after.Type() != input.Type() || after.ArrayRank() != 2 || after.ArrayLen() != 6 || after.ArrayDimension(0) != (sqltype.ArrayDimension{Lower: -2, Length: 2}) || !after.ArrayElement(1).IsNull() {
			t.Fatal("inspection changed input")
		}
	}
	// JSON has no equality provider; shape inspection cannot need one or format elements.
	types := arraySearchTestTypes()
	array := types[types[sqltype.JSON].AssociatedArray()]
	payload, err := array.Input(context.Background(), `{"{}",NULL}`, -1)
	if err != nil {
		t.Fatal(err)
	}
	call := ArrayInspect{Info: expr.Info{Usage: types[sqltype.Int4].Usage()}, Kind: ArrayCardinality, Args: []expr.Expr{arraySearchTestLiteral(payload)}}
	v, err := call.Eval(ctx)
	n, nerr := v.Integer()
	if err != nil || nerr != nil || n != 2 {
		t.Fatal(v, err, nerr)
	}
}
