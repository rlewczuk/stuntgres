package functions

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/sqltype"
	"testing"
)

type transformUnitContext struct{ context.Context }

func (c *transformUnitContext) FunctionContext() context.Context { return c.Context }
func (*transformUnitContext) FunctionInput() sqltype.InputEnvironment {
	return sqltype.InputEnvironment{}
}
func (*transformUnitContext) FunctionStringOutput() sqltype.OutputEnvironment {
	return sqltype.OutputEnvironment{}
}

type transformUnitExpr struct {
	Info[struct{}]
	value sqltype.Value
	order *[]int
	index int
	err   error
}

func (e transformUnitExpr) Eval(*transformUnitContext) (sqltype.Value, error) {
	if e.order != nil {
		*e.order = append(*e.order, e.index)
	}
	return e.value, e.err
}
func TestArrayTransformProviderOrder(t *testing.T) {
	types := arraySearchTestTypes()
	ctx := &transformUnitContext{context.Background()}
	var order []int
	a, err := types[1007].Input(ctx, "{2,1}", -1)
	if err != nil {
		t.Fatal(err)
	}
	arg := func(v sqltype.Value, i int) transformUnitExpr {
		return transformUnitExpr{Info: Info[struct{}]{Usage: v.Type().Usage()}, value: v, order: &order, index: i}
	}
	plan, err := sqltype.BindArraySort(types[1007], types[1007].Usage().Collation, false)
	if err != nil {
		t.Fatal(err)
	}
	call := ArrayTransform[transformUnitExpr, *transformUnitContext, struct{}]{Info: Info[struct{}]{Usage: types[1007].Usage()}, Kind: ArraySort, Args: []transformUnitExpr{arg(sqltype.Boolean(types[sqltype.Bool], false), 1), arg(a, 0), arg(sqltype.Boolean(types[sqltype.Bool], false), 2)}, Slots: [3]uint8{1, 0, 2}, Labels: [3]string{"descending", "array", "nulls_first"}, Sort: plan}
	if err := call.Validate(); err != nil {
		t.Fatal(err)
	}
	v, err := call.Eval(ctx)
	if err != nil || !reflect.DeepEqual(order, []int{0, 1, 2}) {
		t.Fatal(order, err)
	}
	text, err := v.Type().Output(ctx, v)
	if err != nil || text != "{1,2}" {
		t.Fatal(text, err)
	}
	order = nil
	call.Args[1].value = sqltype.Null(types[1007])
	failure := errors.New("flag failure")
	call.Args[0].err = failure
	if _, err := call.Eval(ctx); !errors.Is(err, failure) || !reflect.DeepEqual(order, []int{0, 1}) {
		t.Fatal(order, err)
	}
	order = nil
	call.Args[0].err = nil
	v, err = call.Eval(ctx)
	if err != nil || !v.IsNull() || !reflect.DeepEqual(order, []int{0, 1, 2}) {
		t.Fatal(v, order, err)
	}
	broken := call
	broken.Slots = [3]uint8{0, 0, 2}
	if broken.Validate() == nil {
		t.Fatal("duplicate slots accepted")
	}
	rebuilt, err := call.Reconstruct(call.Info, append([]transformUnitExpr(nil), call.Args...), types[1007].Usage().Collation, false)
	if err != nil || rebuilt.Slots != call.Slots || rebuilt.Labels != call.Labels {
		t.Fatal(rebuilt, err)
	}
}
func TestArrayTransformOutputAndVolatility(t *testing.T) {
	types := arraySearchTestTypes()
	ctx := &transformUnitContext{context.Background()}
	literal := func(v sqltype.Value) transformUnitExpr {
		return transformUnitExpr{Info: Info[struct{}]{Usage: v.Type().Usage()}, value: v}
	}
	a, err := types[1007].Input(ctx, "{{1,NULL},{2,3}}", -1)
	if err != nil {
		t.Fatal(err)
	}
	call := ArrayTransform[transformUnitExpr, *transformUnitContext, struct{}]{Info: Info[struct{}]{Usage: types[sqltype.Text].Usage()}, Kind: ArrayToString, Slots: [3]uint8{0, 1, 2}, Args: []transformUnitExpr{literal(a), literal(sqltype.String(types[sqltype.Text], ",")), literal(sqltype.String(types[sqltype.Text], "*"))}}
	if err := call.Validate(); err != nil || !call.IntrinsicStable() {
		t.Fatal(err)
	}
	v, err := call.Eval(ctx)
	text, terr := v.Text()
	if err != nil || terr != nil || text != "1,*,2,3" {
		t.Fatal(text, err, terr)
	}
	call.Args[2] = literal(sqltype.Null(types[sqltype.Text]))
	v, err = call.Eval(ctx)
	text, terr = v.Text()
	if err != nil || terr != nil || text != "1,2,3" {
		t.Fatal(text, err, terr)
	}
	pseudo := types[2277].Usage()
	for _, kind := range []ArrayTransformKind{ArrayReverse, ArraySort, ArrayTrim, ArrayToString} {
		args := []transformUnitExpr{literal(sqltype.CatalogResultNull(pseudo))}
		result := pseudo
		if kind == ArrayTrim {
			zero, _ := sqltype.Integer(types[sqltype.Int4], 0)
			args = append(args, literal(zero))
		}
		if kind == ArrayToString {
			result = types[sqltype.Text].Usage()
			args = append(args, literal(sqltype.String(types[sqltype.Text], ",")))
		}
		c := ArrayTransform[transformUnitExpr, *transformUnitContext, struct{}]{Info: Info[struct{}]{Usage: result}, Kind: kind, Args: args, Slots: [3]uint8{0, 1, 2}}
		if err := c.Validate(); err != nil {
			t.Fatal(kind, err)
		}
		v, err := c.Eval(ctx)
		if err != nil || !v.IsNull() || v.Type() != result.Type {
			t.Fatal(kind, v, err)
		}
	}
}
