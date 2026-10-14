package functions

import (
	"context"
	"errors"
	"stuntgres/internal/sqltype"
	"testing"
)

type nullCountUnitContext struct{ context.Context }

func (c *nullCountUnitContext) FunctionContext() context.Context { return c.Context }
func (*nullCountUnitContext) WholeRowNullPresence(nullCountUnitExpression) (bool, bool, error) {
	return false, false, nil
}

type nullCountUnitExpression interface {
	ResultUsage() sqltype.Usage
	Eval(*nullCountUnitContext) (sqltype.Value, error)
}
type nullCountUnitArgument struct {
	Info[struct{}]
	value sqltype.Value
	err   error
	calls *int
}

func (w nullCountUnitArgument) Eval(*nullCountUnitContext) (sqltype.Value, error) {
	if w.calls != nil {
		*w.calls++
	}
	return w.value, w.err
}
func TestNullCountTypedEvaluation(t *testing.T) {
	types := formatUnitTypes()
	ctx := context.Background()
	var args []nullCountUnitExpression
	for _, tc := range []struct {
		id    sqltype.ID
		input string
	}{
		{sqltype.Int2, "0"}, {sqltype.Int4, "0"}, {sqltype.Int8, "2147483648"}, {sqltype.Text, ""}, {sqltype.Bool, "false"},
		{sqltype.Numeric, "1.5"}, {sqltype.Float4, "1.5"}, {sqltype.Float8, "2.5"}, {sqltype.Time, "12:34:56"},
		{sqltype.Timestamp, "2000-01-02 03:04:05"}, {sqltype.UUID, "00000000-0000-0000-0000-000000000001"},
	} {
		typ := types[tc.id]
		value, err := typ.Input(ctx, tc.input, -1)
		if err != nil {
			t.Fatal(tc, err)
		}
		args = append(args, nullCountUnitArgument{Info: Info[struct{}]{Usage: typ.Usage()}, value: value}, nullCountUnitArgument{Info: Info[struct{}]{Usage: typ.Usage()}, value: sqltype.Null(typ)})
	}
	for _, kind := range []NullCountKind{CountNulls, CountNonnulls} {
		call := NullCount[nullCountUnitExpression, *nullCountUnitContext, struct{}]{Info: Info[struct{}]{Usage: types[sqltype.Int4].Usage()}, Kind: kind, Args: args}
		for range 2 {
			value, err := call.Eval(&nullCountUnitContext{Context: ctx})
			n, _ := value.Integer()
			if err != nil || value.IsNull() || value.Type() != types[sqltype.Int4] || n != 11 {
				t.Fatal(value, err)
			}
		}
		evaluation := &nullCountUnitContext{Context: ctx}
		if allocations := testing.AllocsPerRun(10, func() {
			if _, err := call.Eval(evaluation); err != nil {
				panic(err)
			}
		}); allocations != 0 {
			t.Fatal("per-Eval allocation", allocations)
		}
	}
	calls := 0
	sentinel := errors.New("child failure")
	typ := types[sqltype.Text]
	first := nullCountUnitArgument{Info: Info[struct{}]{Usage: typ.Usage()}, value: sqltype.Null(typ), calls: &calls}
	last := nullCountUnitArgument{Info: Info[struct{}]{Usage: typ.Usage()}, value: sqltype.String(typ, ""), calls: &calls}
	call := NullCount[nullCountUnitExpression, *nullCountUnitContext, struct{}]{Info: Info[struct{}]{Usage: types[sqltype.Int4].Usage()}, Kind: CountNulls, Args: []nullCountUnitExpression{first, last}}
	value, err := call.Eval(&nullCountUnitContext{Context: ctx})
	n, _ := value.Integer()
	if err != nil || n != 1 || calls != 2 {
		t.Fatal(value, err, calls)
	}
	last.err = sentinel
	call.Args[1] = last
	calls = 0
	if _, err := call.Eval(&nullCountUnitContext{Context: ctx}); !errors.Is(err, sentinel) || calls != 2 {
		t.Fatal(err, calls)
	}
	first.err = sentinel
	call.Args[0] = first
	calls = 0
	if _, err := call.Eval(&nullCountUnitContext{Context: ctx}); !errors.Is(err, sentinel) || calls != 1 {
		t.Fatal("first error", err, calls)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	calls = 0
	if _, err := call.Eval(&nullCountUnitContext{Context: canceled}); !errors.Is(err, context.Canceled) || calls != 0 {
		t.Fatal(err, calls)
	}
	first.err = nil
	first.value = sqltype.Value{}
	call.Args = []nullCountUnitExpression{first}
	if value, err := call.Eval(&nullCountUnitContext{Context: ctx}); err == nil || value.Type() != nil {
		t.Fatal("zero value counted", value, err)
	}
	first.value = sqltype.Null(types[sqltype.Int4])
	call.Args[0] = first
	if value, err := call.Eval(&nullCountUnitContext{Context: ctx}); err == nil || value.Type() != nil {
		t.Fatal("type mismatch counted", value, err)
	}
	if _, err := (NullCount[nullCountUnitExpression, *nullCountUnitContext, struct{}]{}).Eval(&nullCountUnitContext{Context: ctx}); err == nil {
		t.Fatal("zero call accepted")
	}
}
