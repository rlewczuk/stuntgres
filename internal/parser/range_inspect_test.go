package parser

import (
	"strings"
	"testing"
)

func TestRangeInspectParserAdmission(t *testing.T) {
	for _, name := range []string{"lower", "upper", "isempty", "lower_inc", "upper_inc", "lower_inf", "upper_inf", "range_merge"} {
		for _, q := range []string{
			"SELECT " + name + "(v) FROM r",
			"SELECT pg_catalog." + name + "(v) FROM r",
			"SELECT " + name + "(DISTINCT v) FROM r",
			"SELECT " + name + "(v ORDER BY k) FROM r",
			"SELECT " + name + "(v) FILTER (WHERE true) FROM r",
			"SELECT " + name + "(v) OVER () FROM r",
			"SELECT " + name + "(x => v) FROM r",
			"SELECT " + name + "(VARIADIC v) FROM r",
		} {
			statements, err := Parse(q)
			if err != nil || len(statements) != 1 {
				t.Fatal(q, err)
			}
			call, ok := statements[0].(*Select).Targets[0].(FunctionExpr)
			if !ok || call.Name.Parts[len(call.Name.Parts)-1].Text != name {
				t.Fatal("call identity", q)
			}
		}
		upper := strings.ToUpper(name)
		statements, err := Parse(`SELECT "` + upper + `"(v) FROM r`)
		if err != nil {
			t.Fatal(err)
		}
		call := statements[0].(*Select).Targets[0].(FunctionExpr)
		if call.Name.Parts[0].Text != upper {
			t.Fatal("quoted spelling lost")
		}
	}
	for _, q := range []string{
		"SELECT lower(range_merge(a,b)) FROM r",
		"CREATE TABLE r(v int4range,g integer GENERATED ALWAYS AS (lower(range_merge(v,v))) STORED,CHECK (isempty(v) OR lower_inc(v)))",
		"CREATE VIEW r_view AS SELECT range_merge(v,v) AS hull FROM r",
	} {
		if statements, err := Parse(q); err != nil || len(statements) != 1 {
			t.Fatal(q, err)
		}
	}
	if RangeInspectFunctionName("range_agg") || RangeInspectFunctionName("unnest") || RangeInspectFunctionName("int4range") {
		t.Fatal("adjacent admission")
	}
}
