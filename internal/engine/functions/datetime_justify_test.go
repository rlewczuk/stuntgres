package functions

import (
	"context"
	"errors"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestDatetimeJustifySelectedPlanExecution(t *testing.T) {
	d := temporalTestTypes()[sqltype.Interval]
	for _, name := range []string{"justify_days", "justify_hours", "justify_interval"} {
		plan, err := sqltype.BindDatetimeJustify(name, d, d)
		if err != nil {
			t.Fatal(err)
		}
		value, err := sqltype.IntervalValue(d, 0, 30, 0)
		if err != nil {
			t.Fatal(err)
		}
		e := DatetimeJustify{Info: expr.Info{Usage: d.Usage()}, Plan: plan, Arg: temporalTestLiteral(value)}
		v, err := e.Eval(&temporalTestContext{Context: context.Background()})
		wantMonths, wantDays := int32(1), int32(0)
		if name == "justify_hours" {
			wantMonths, wantDays = 0, 30
		}
		m, days, clock, failure := v.Interval()
		if err != nil || failure != nil || m != wantMonths || days != wantDays || clock != 0 {
			t.Fatal(v, err, failure)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := e.Eval(&temporalTestContext{Context: ctx}); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	}
}
