package functions

import (
	"context"
	"errors"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestDatetimeTruncPrecancellation(t *testing.T) {
	d := temporalTestTypes()
	plan, err := sqltype.BindDatetimeTrunc(d[sqltype.Text], d[sqltype.Timestamptz], d[sqltype.Timestamptz], d[sqltype.Text])
	if err != nil {
		t.Fatal(err)
	}
	e := DatetimeTrunc{Info: expr.Info{Usage: d[sqltype.Timestamptz].Usage()}, Plan: plan, Count: 3, Args: [3]expr.Expr{temporalTestLiteral(sqltype.String(d[sqltype.Text], "day")), temporalTestLiteral(temporalTestTimestamp(t, d[sqltype.Timestamptz], 0)), temporalTestLiteral(sqltype.String(d[sqltype.Text], "UTC"))}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := e.Eval(&temporalTestContext{Context: ctx}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
