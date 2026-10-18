package functions

import (
	"context"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestDatetimeBinSelectedPlanAlignment(t *testing.T) {
	d := temporalTestTypes()
	for _, id := range []sqltype.ID{sqltype.Timestamp, sqltype.Timestamptz} {
		plan, err := sqltype.BindDatetimeBin(d[sqltype.Interval], d[id], d[id])
		if err != nil {
			t.Fatal(err)
		}
		stride, err := sqltype.IntervalValue(d[sqltype.Interval], 0, 0, 900000000)
		if err != nil {
			t.Fatal(err)
		}
		e := DatetimeBin{Info: expr.Info{Usage: d[id].Usage()}, Plan: plan, Args: [3]expr.Expr{temporalTestLiteral(stride), temporalTestLiteral(temporalTestTimestamp(t, d[id], 149999999)), temporalTestLiteral(temporalTestTimestamp(t, d[id], 150000000))}}
		value, err := e.Eval(&temporalTestContext{Context: context.Background()})
		n, failure := value.Timestamp()
		if err != nil || failure != nil || n != -750000000 {
			t.Fatal(value, err, failure)
		}
	}
}
