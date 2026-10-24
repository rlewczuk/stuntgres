package parser

import "testing"

func TestIntaggScalarInputPrecedence(t *testing.T) {
	for _, query := range []string{
		"INSERT INTO ia_values VALUES(ia_addon.int_array_aggregate(1))",
		"INSERT INTO ia_values VALUES(ia_addon.int_array_aggregate('bad'))",
		"INSERT INTO ia_values VALUES(ia_addon.int_array_aggregate(1::bigint))",
		"INSERT INTO ia_values VALUES(absent.int_array_aggregate(missing))",
		"INSERT INTO ia_values VALUES(ia_addon.int_array_aggregate(1) FILTER(WHERE missing))",
	} {
		statements, err := Parse(query)
		if err != nil || len(statements) != 1 {
			t.Fatalf("installed aggregate analysis was preempted by scalar-input parsing: %s: %v", query, err)
		}
	}
}
