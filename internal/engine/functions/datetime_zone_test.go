package functions

import (
	"context"
	"errors"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestDatetimeZoneCalendarLiteralNullAndPrecancellation(t *testing.T) {
	d := temporalTestTypes()
	provider, err := sqltype.BindArithmetic(sqltype.ArithmeticAdd, d[sqltype.Timestamptz], d[sqltype.Interval], d[sqltype.Timestamptz])
	if err != nil {
		t.Fatal(err)
	}
	duration, err := sqltype.IntervalValue(d[sqltype.Interval], 0, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	e := DatetimeZoneCalendar{Info: expr.Info{Usage: d[sqltype.Timestamptz].Usage()}, Name: "date_add", Count: 3, Provider: provider, Args: [3]expr.Expr{temporalTestLiteral(sqltype.Null(d[sqltype.Timestamptz])), temporalTestLiteral(duration), temporalTestLiteral(sqltype.String(d[sqltype.Text], "No/Such_Zone"))}}
	value, err := e.Eval(&temporalTestContext{Context: context.Background()})
	if err != nil || !value.IsNull() || value.Type() != e.ResultUsage().Type {
		t.Fatal(value, err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := e.Eval(&temporalTestContext{Context: canceled}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
