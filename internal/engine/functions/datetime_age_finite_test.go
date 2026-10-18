package functions

import (
	"context"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
	"testing"
	"time"
)

func TestDatetimeAgeFiniteSelectedPlanExecution(t *testing.T) {
	d := temporalTestTypes()
	zone, err := sqltype.ResolveZone("UTC")
	if err != nil {
		t.Fatal(err)
	}
	input := sqltype.InputEnvironment{Zone: zone, TransactionStart: time.Date(2000, 1, 1, 12, 0, 0, 0, time.UTC)}
	for _, tc := range []struct {
		name   string
		source sqltype.ID
		values []string
		result sqltype.ID
	}{
		{"age", sqltype.Timestamp, []string{"2000-01-01"}, sqltype.Interval},
		{"age", sqltype.Timestamp, []string{"2004-06-01", "2004-04-30"}, sqltype.Interval},
		{"isfinite", sqltype.Date, []string{"infinity"}, sqltype.Bool},
		{"isfinite", sqltype.Timestamp, []string{"infinity"}, sqltype.Bool},
		{"isfinite", sqltype.Interval, []string{"infinity"}, sqltype.Bool},
	} {
		plan, err := sqltype.BindDatetimeAgeFinite(tc.name, d[tc.source], d[tc.result], d[sqltype.Date], len(tc.values))
		if err != nil {
			t.Fatal(err)
		}
		e := DatetimeAgeFinite{Info: expr.Info{Usage: d[tc.result].Usage()}, Plan: plan, Count: len(tc.values)}
		for i, text := range tc.values {
			v, err := d[tc.source].InputWithEnvironment(context.Background(), text, -1, input)
			if err != nil {
				t.Fatal(err)
			}
			e.Args[i] = temporalTestLiteral(v)
		}
		value, err := e.Eval(&temporalTestContext{Context: context.Background(), input: input})
		if err != nil || value.Type() != d[tc.result] || value.IsNull() {
			t.Fatal("selected evaluation", tc, value, err)
		}
		if tc.name == "age" {
			wantMonths, wantDays := int32(0), int32(0)
			if len(tc.values) == 2 {
				wantMonths, wantDays = 1, 1
			}
			months, days, clock, err := value.Interval()
			if err != nil || months != wantMonths || days != wantDays || clock != 0 {
				t.Fatal(tc, value, err)
			}
		} else {
			finite, err := value.Boolean()
			if err != nil || finite {
				t.Fatal(tc, value, err)
			}
		}
	}
}
