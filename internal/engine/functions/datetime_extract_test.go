package functions

import (
	"context"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestDatetimeExtractSelectedPlanExecution(t *testing.T) {
	d := temporalTestTypes()
	zone, err := sqltype.ResolveZone("UTC")
	if err != nil {
		t.Fatal(err)
	}
	input := sqltype.InputEnvironment{Zone: zone}
	expected := map[sqltype.ID]struct {
		numeric  string
		floating float64
	}{
		sqltype.Date:        {"946684800", 946684800},
		sqltype.Time:        {"43200.000000", 43200},
		sqltype.Timetz:      {"22500.000000", 22500},
		sqltype.Timestamp:   {"946684800.000000", 946684800},
		sqltype.Timestamptz: {"946684800.000000", 946684800},
		sqltype.Interval:    {"37015200.000000", 37015200},
	}
	for _, source := range []sqltype.ID{sqltype.Date, sqltype.Time, sqltype.Timetz, sqltype.Timestamp, sqltype.Timestamptz, sqltype.Interval} {
		text := "2000-01-01"
		switch source {
		case sqltype.Time:
			text = "12:00"
		case sqltype.Timetz:
			text = "12:00+05:45"
		case sqltype.Interval:
			text = "1 year 2 months 3 days 4 hours"
		}
		value, err := d[source].InputWithEnvironment(context.Background(), text, -1, input)
		if err != nil {
			t.Fatal(err)
		}
		for _, api := range []string{"extract", "date_part"} {
			result := d[sqltype.Numeric]
			if api == "date_part" {
				result = d[sqltype.Float8]
			}
			plan, err := sqltype.BindDatetimeExtract(api, d[sqltype.Text], d[source], result, d[sqltype.Timestamp])
			if err != nil {
				t.Fatal(err)
			}
			e := DatetimeExtract{Info: expr.Info{Usage: result.Usage()}, Plan: plan, Args: [2]expr.Expr{temporalTestLiteral(sqltype.String(d[sqltype.Text], "epoch")), temporalTestLiteral(value)}}
			actual, err := e.Eval(&temporalTestContext{Context: context.Background(), input: input})
			if err != nil {
				t.Fatal(source, api, err)
			}
			if actual.Type() != result || actual.IsNull() {
				t.Fatal(source, api, "result authority", actual)
			}
			if api == "extract" {
				text, err := result.Output(context.Background(), actual)
				if err != nil || text != expected[source].numeric {
					t.Fatal(source, api, text, err)
				}
			} else {
				number, err := actual.Float()
				if err != nil || number != expected[source].floating {
					t.Fatal(source, api, number, err)
				}
			}
		}
	}
}
