package functions

import (
	"context"
	"errors"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

type conversionUnitExpression interface {
	ResultUsage() sqltype.Usage
	Eval(*conversionUnitContext) (sqltype.Value, error)
}
type conversionUnitContext struct {
	context.Context
	handler    *sqltype.EncodingConversionHandler
	ownerError error
	denyLookup bool
}

func (c *conversionUnitContext) FunctionContext() context.Context { return c.Context }
func (c *conversionUnitContext) FunctionLiteralNull(arg conversionUnitExpression) bool {
	value, ok := arg.(conversionUnitArgument)
	return ok && value.literal
}
func (c *conversionUnitContext) FunctionBinaryConversion(source, destination int) (*sqltype.EncodingConversionHandler, error) {
	if c.denyLookup {
		panic("empty or identity conversion demanded owner capability")
	}
	if c.ownerError != nil {
		return nil, c.ownerError
	}
	if c.handler != nil && !c.handler.Supports(source, destination) {
		panic("unexpected conversion direction")
	}
	return c.handler, nil
}

type conversionUnitArgument struct {
	Info[struct{}]
	value   sqltype.Value
	order   *[]int
	index   int
	literal bool
}

func (a conversionUnitArgument) Eval(*conversionUnitContext) (sqltype.Value, error) {
	if a.order != nil {
		*a.order = append(*a.order, a.index)
	}
	return a.value, nil
}
func conversionUnitCall(t *testing.T, name string, ids []sqltype.ID, result sqltype.ID, raw []string, order *[]int) Binary[conversionUnitExpression, *conversionUnitContext, struct{}] {
	t.Helper()
	types := formatUnitTypes()
	args := make([]conversionUnitExpression, len(ids))
	usages := make([]sqltype.Usage, len(ids))
	for i, id := range ids {
		source := types[id]
		usages[i] = source.Usage()
		var value sqltype.Value
		if id == sqltype.Bytea {
			var err error
			value, err = sqltype.Binary(source, raw[i])
			if err != nil {
				t.Fatal(err)
			}
		} else {
			value = sqltype.String(source, raw[i])
		}
		args[i] = conversionUnitArgument{Info: Info[struct{}]{Usage: source.Usage()}, value: value, order: order, index: i}
	}
	usage := types[result].Usage()
	plan, err := sqltype.BindBinary(name, usage, usages...)
	if err != nil {
		t.Fatal(err)
	}
	return Binary[conversionUnitExpression, *conversionUnitContext, struct{}]{Info: Info[struct{}]{Usage: usage}, Plan: plan, Args: args}
}
func TestBinaryConversionEmptyAndIdentity(t *testing.T) {
	for _, tc := range []struct{ source, destination, want string }{{"", "LATIN1", ""}, {"abc", "UTF8", "abc"}} {
		node := conversionUnitCall(t, "convert_to", []sqltype.ID{sqltype.Text, sqltype.Name}, sqltype.Bytea, []string{tc.source, tc.destination}, nil)
		value, err := node.Eval(&conversionUnitContext{Context: context.Background(), denyLookup: true})
		if err != nil {
			t.Fatal(err)
		}
		raw, err := value.Binary()
		if err != nil || raw != tc.want {
			t.Fatal(raw, err)
		}
	}
}
func TestBinaryConversionMissingAndOwnerError(t *testing.T) {
	node := conversionUnitCall(t, "convert_to", []sqltype.ID{sqltype.Text, sqltype.Name}, sqltype.Bytea, []string{"é", "LATIN1"}, nil)
	_, err := node.Eval(&conversionUnitContext{Context: context.Background()})
	nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "42883", Message: `default conversion function for encoding "UTF8" to "LATIN1" does not exist`})
	ownerError := errors.New("actual owner lookup failed")
	value, err := node.Eval(&conversionUnitContext{Context: context.Background(), ownerError: ownerError})
	if err != ownerError || value.Type() != nil {
		t.Fatal(value, err)
	}
}
