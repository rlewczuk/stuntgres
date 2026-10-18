package functions

import (
	"context"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
	"time"
)

type currentDateTimeUnitContext struct {
	unitCapabilities
	context.Context
	input sqltype.InputEnvironment
}

func (c *currentDateTimeUnitContext) FunctionContext() context.Context {
	if c == nil {
		return nil
	}
	return c.Context
}
func (c *currentDateTimeUnitContext) FunctionInput() sqltype.InputEnvironment { return c.input }
func TestCurrentDateTimeInputDemand(t *testing.T) {
	types := formatUnitTypes()
	ctx := context.Background()
	for _, kind := range []parser.CurrentDateTimeKind{parser.CurrentDate, parser.CurrentTime, parser.CurrentTimestamp, parser.LocalTime, parser.LocalTimestamp} {
		raw := parser.CurrentDateTimeExpr{Kind: kind}
		if kind != parser.CurrentDate {
			raw.HasPrecision = true
			raw.Precision = 3
		}
		usage, _, err := ResolveCurrentDateTimeUsage(raw, types[CurrentDateTimeType(kind)].Usage())
		if err != nil {
			t.Fatal(err)
		}
		node := CurrentDateTime{Info: expr.Info{Usage: usage}, Kind: kind, HasPrecision: raw.HasPrecision, Precision: usage.Typmod}
		if _, err := node.Eval((*currentDateTimeUnitContext)(nil)); err == nil {
			t.Fatal("missing evaluation context accepted")
		}
		if _, err := node.Eval(&currentDateTimeUnitContext{Context: ctx}); err == nil {
			t.Fatal("missing transaction clock accepted")
		}
		stamp := time.Date(2000, 1, 1, 12, 0, 0, 123500000, time.UTC)
		value, err := node.Eval(&currentDateTimeUnitContext{Context: ctx, input: sqltype.InputEnvironment{TransactionStart: stamp}})
		if err != nil || value.Type() != node.ResultUsage().Type {
			t.Fatal(value, err)
		}
	}
}
