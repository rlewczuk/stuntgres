package functions

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type recordProviderContext struct {
	unitCapabilities
	context.Context
	input sqltype.InputEnvironment
}

func (c *recordProviderContext) FunctionContext() context.Context                   { return c.Context }
func (c *recordProviderContext) FunctionJSONBRecordInput() sqltype.InputEnvironment { return c.input }

type recordProviderArgument struct {
	expr.Info
	value   sqltype.Value
	order   *[]int
	index   int
	failure error
}

func (a recordProviderArgument) Eval(expr.Context) (sqltype.Value, error) {
	if a.order != nil {
		*a.order = append(*a.order, a.index)
	}
	return a.value, a.failure
}

func TestJSONBRecordProvider(t *testing.T) {
	types := formatUnitTypes()
	ctx := &recordProviderContext{Context: context.Background()}
	shape, err := sqltype.RecordShape([]sqltype.CompositeMember{{Attribute: 1, Name: "a", Usage: types[sqltype.Int4].Usage()}, {Attribute: 2, Name: "b", Usage: types[sqltype.Text].Usage()}})
	if err != nil {
		t.Fatal(err)
	}
	input := func(text string) sqltype.Value {
		t.Helper()
		v, err := types[sqltype.JSONB].Input(ctx, text, -1)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	baseFields := []sqltype.Value{sqltype.Null(types[sqltype.Int4]), sqltype.String(types[sqltype.Text], "keep")}
	base, err := sqltype.OwnedCompositeValue(shape, baseFields)
	if err != nil {
		t.Fatal(err)
	}
	var order []int
	arg := func(index int, value sqltype.Value) expr.Expr {
		return recordProviderArgument{Info: expr.Info{Usage: value.Type().Usage()}, value: value, index: index, order: &order}
	}
	call := JSONBRecord{Info: expr.Info{Usage: shape.Usage()}, Name: "jsonb_populate_record", Args: []expr.Expr{arg(0, base), arg(1, input(`{"a":1}`))}}
	value, err := call.Eval(ctx)
	if err != nil || !reflect.DeepEqual(order, []int{0, 1}) {
		t.Fatal(value, order, err)
	}
	if got, err := shape.Output(ctx, value); err != nil || got != "(1,keep)" {
		t.Fatal(got, err)
	}
	call.Name = "jsonb_populate_record_valid"
	call.Info.Usage = types[sqltype.Bool].Usage()
	call.Args[1] = arg(1, input(`{"a":"bad"}`))
	value, err = call.Eval(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if valid, err := value.Boolean(); err != nil || valid {
		t.Fatal(valid, err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := call.Eval(&recordProviderContext{Context: cancelled}); !errors.Is(err, context.Canceled) {
		t.Fatal("valid swallowed cancellation", err)
	}
	call.Args[0] = recordProviderArgument{Info: expr.Info{Usage: shape.Usage()}, failure: context.Canceled}
	if _, err := call.Eval(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal("valid swallowed argument failure", err)
	}
	named, err := sqltype.Composite(50000, "record_provider_named", "public.record_provider_named", 50001, []sqltype.CompositeMember{shape.CompositeMember(0), shape.CompositeMember(1)})
	if err != nil {
		t.Fatal(err)
	}
	call.Args[0] = arg(0, sqltype.Null(named))
	call.Args[1] = arg(1, sqltype.Null(types[sqltype.JSONB]))
	value, err = call.Eval(ctx)
	if err != nil || !value.IsNull() || value.Type() != types[sqltype.Bool] {
		t.Fatal("valid NULL contract", value, err)
	}
	call.Args[0] = arg(0, sqltype.Null(shape))
	call.Unresolved = true
	if _, err := call.Eval(ctx); err == nil || sqltype.InputSoftError(err) {
		t.Fatal("unresolved setup softened", err)
	}
	call.Unresolved = false
	call.Name = "jsonb_populate_recordset"
	call.Info.Usage = shape.Usage()
	call.Args[0] = arg(0, base)
	call.Args[1] = arg(1, input(`[{"a":1},{"a":2}]`))
	order = nil
	rows, err := call.EvalSet(ctx)
	if err != nil || len(rows) != 2 || !reflect.DeepEqual(order, []int{0, 1}) {
		t.Fatal(rows, order, err)
	}
	other, err := call.EvalSet(ctx)
	if err != nil || len(other) != 2 || &rows[0] == &other[0] {
		t.Fatal("shared invocation buffer", err)
	}
	call.Args[1] = arg(1, input(`[{"a":1},{"a":"bad"}]`))
	if rows, err := call.EvalSet(ctx); rows != nil || err == nil {
		t.Fatal("partial provider result", rows, err)
	}
	call.Args[1] = arg(1, input(`[]`))
	if rows, err := call.EvalSet(ctx); len(rows) != 0 || err != nil {
		t.Fatal(rows, err)
	}
	if _, err := call.Reconstruct(call.Info, nil); err == nil {
		t.Fatal("missing arguments reconstructed")
	}
	// Setup failures preserve their original SQLSTATE and are not valid=false.
	call.Name = "jsonb_populate_record_valid"
	call.Info.Usage = types[sqltype.Bool].Usage()
	call.Args[0] = arg(0, sqltype.Null(types[sqltype.Int4]))
	_, err = call.Eval(ctx)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "42804" {
		t.Fatal(err)
	}
	// Caller layout applies to an evaluated NULL, never to the inner expression.
	empty, err := sqltype.RecordShape(nil)
	if err != nil {
		t.Fatal(err)
	}
	inner := JSONBRecord{
		Info: expr.Info{Usage: empty.Usage()}, Name: "jsonb_to_record", Unresolved: true,
		Args: []expr.Expr{arg(5, sqltype.Null(types[sqltype.JSONB]))},
	}
	outer := JSONBRecord{
		Info: expr.Info{Usage: shape.Usage()}, Name: "jsonb_populate_record", Definitions: true,
		Args: []expr.Expr{inner, arg(7, input(`{"a":1}`))},
	}
	outer, err = outer.Reconstruct(outer.Info, outer.Args)
	if err != nil {
		t.Fatal(err)
	}
	order = nil
	value, err = outer.Eval(ctx)
	if err != nil || value.Type() != shape || !reflect.DeepEqual(order, []int{5, 7}) {
		t.Fatal("context erased evaluation", value, order, err)
	}
	if n, err := value.CompositeField(0).Integer(); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	inner.Args[0] = arg(5, input(`{}`))
	outer.Args[0] = inner
	order = nil
	if _, err := outer.Eval(ctx); !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" || diagnostic.Message != "could not determine row type for result of jsonb_to_record" || !reflect.DeepEqual(order, []int{5}) {
		t.Fatal("outer definition supplied inner layout or skipped its failure", order, err)
	}
	inner.Args[0] = arg(5, sqltype.Null(types[sqltype.JSONB]))
	middle := JSONBRecord{
		Info: expr.Info{Usage: empty.Usage()}, Name: "jsonb_populate_record", Unresolved: true,
		Args: []expr.Expr{inner, arg(6, input(`{}`))},
	}
	outer.Args[0] = middle
	order = nil
	if _, err := outer.Eval(ctx); !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" || diagnostic.Message != "could not determine row type for result of jsonb_populate_record" || !reflect.DeepEqual(order, []int{5, 6}) {
		t.Fatal("caller descriptor leaked into middle expression", order, err)
	}
	runtimeShape, err := sqltype.RecordShape([]sqltype.CompositeMember{{Attribute: 1, Name: "f1", Usage: types[sqltype.Int4].Usage()}, {Attribute: 2, Name: "f2", Usage: types[sqltype.Text].Usage()}})
	if err != nil {
		t.Fatal(err)
	}
	runtimeBase, err := sqltype.OwnedCompositeValue(runtimeShape, []sqltype.Value{sqltype.Null(types[sqltype.Int4]), sqltype.String(types[sqltype.Text], "keep")})
	if err != nil {
		t.Fatal(err)
	}
	outer.Args = []expr.Expr{arg(0, runtimeBase), arg(1, input(`{"f1":1}`))}
	value, err = outer.Eval(ctx)
	if err != nil || value.Type() != shape {
		t.Fatal("caller ABI replaced actual conversion layout", value, err)
	}
	if text, err := shape.Output(ctx, value); err != nil || text != "(1,keep)" {
		t.Fatal(text, err)
	}
	runtimeEmpty, err := sqltype.OwnedCompositeValue(empty, []sqltype.Value{})
	if err != nil {
		t.Fatal(err)
	}
	outer.Args[0] = arg(0, runtimeEmpty)
	outer.Args[1] = arg(1, input(`{}`))
	if _, err := outer.Eval(ctx); !errors.As(err, &diagnostic) || diagnostic.Code != "42804" || diagnostic.Detail != "Returned row contains 0 attributes, but query expects 2." {
		t.Fatal("caller arity silently replaced real record", err)
	}
}
