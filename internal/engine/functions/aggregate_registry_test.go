package functions

import "testing"

func TestAggregateDefinitionsAreWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, definition := range aggregateDefinitions {
		if len(definition.Names) == 0 || (definition.Bind == nil) == (definition.BindReturning == nil) {
			t.Fatalf("incomplete aggregate definition %v", definition.Names)
		}
		for _, name := range definition.Names {
			// The parser folds aggregate names; matching is exact.
			if name == "" || seen[name] || name != toLowerASCII(name) {
				t.Fatalf("empty, duplicate or non-lowercase aggregate name %q", name)
			}
			seen[name] = true
		}
	}
}

func toLowerASCII(s string) string {
	b := []byte(s)
	for i, c := range b {
		if 'A' <= c && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	return string(b)
}

func TestLookupAggregateClaimsRegisteredNames(t *testing.T) {
	claimed := []string{
		"count", "sum", "avg", "min", "max", "any_value", "bool_and", "bool_or", "every",
		"bit_and", "bit_or", "bit_xor", "string_agg", "array_agg", "range_agg", "range_intersect_agg",
		"var_pop", "var_samp", "variance", "stddev_pop", "stddev_samp", "stddev", "regr_slope", "regr_sxx",
		"regr_sxy", "regr_syy", "corr", "covar_pop", "covar_samp", "regr_avgx", "regr_avgy", "regr_count",
		"regr_intercept", "regr_r2", "json_agg", "jsonb_agg", "json_agg_strict", "jsonb_agg_strict", "json_arrayagg",
		"json_object_agg", "jsonb_object_agg", "json_object_agg_strict", "jsonb_object_agg_strict", "json_object_agg_unique",
		"jsonb_object_agg_unique", "json_object_agg_unique_strict", "jsonb_object_agg_unique_strict", "json_objectagg",
	}
	for _, name := range claimed {
		if _, ok := LookupAggregate(name); !ok {
			t.Errorf("aggregate %q is not claimed", name)
		}
	}
	// Matching is exact; the engine keeps intagg and every unported family.
	for _, name := range []string{"SUM", "Count", "Array_Agg", "int_array_aggregate", "percentile_cont", "mode", "rank", "row_number", "JSON_AGG", "json_object", "jsonb_build_object", "json_array", "sum_", "regr"} {
		if _, ok := LookupAggregate(name); ok {
			t.Errorf("aggregate %q is claimed", name)
		}
	}
	for _, name := range claimed {
		definition, _ := LookupAggregate(name)
		values := name == "sum" || name == "bit_and" || name == "bit_or" || name == "bit_xor"
		if definition.ValuesNamespace != values {
			t.Errorf("aggregate %q ValuesNamespace = %v", name, definition.ValuesNamespace)
		}
		if (definition.Window == nil) != (name == "count") {
			t.Errorf("aggregate %q: window signature presence is wrong", name)
		}
	}
}
