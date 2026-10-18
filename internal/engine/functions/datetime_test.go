package functions

import (
	"context"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
	"testing"
)

type temporalTestContext struct {
	unitCapabilities
	context.Context
	input  sqltype.InputEnvironment
	output sqltype.OutputEnvironment
}

func (c *temporalTestContext) FunctionContext() context.Context          { return c.Context }
func (c *temporalTestContext) FunctionInput() sqltype.InputEnvironment   { return c.input }
func (c *temporalTestContext) FunctionOutput() sqltype.OutputEnvironment { return c.output }
func (c *temporalTestContext) FunctionLiteralNull(e expr.Expr) bool {
	a, ok := e.(temporalTestArgument)
	return ok && a.literal && a.value.IsNull()
}
func (c *temporalTestContext) FunctionOverlapsConstant(e expr.Expr) bool {
	a, ok := e.(temporalTestArgument)
	return ok && a.literal
}

type temporalTestArgument struct {
	expr.Info
	value   sqltype.Value
	order   *[]int
	index   int
	err     error
	literal bool
}

func (a temporalTestArgument) Eval(expr.Context) (sqltype.Value, error) {
	if a.order != nil {
		*a.order = append(*a.order, a.index)
	}
	return a.value, a.err
}
func temporalTestTypes() map[sqltype.ID]*sqltype.Descriptor {
	d := map[sqltype.ID]*sqltype.Descriptor{}
	for _, v := range sqltype.Builtins() {
		d[v.ID()] = v
	}
	return d
}
func temporalTestLiteral(v sqltype.Value) temporalTestArgument {
	return temporalTestArgument{Info: expr.Info{Usage: v.Type().Usage()}, value: v, literal: true}
}
func temporalTestTimestamp(t *testing.T, d *sqltype.Descriptor, micros int64) sqltype.Value {
	t.Helper()
	v, err := sqltype.TimestampValue(d, micros)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
