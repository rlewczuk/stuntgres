package functions

import (
	"testing"

	"stuntgres/internal/parser"
)

func TestStoredProviderCallShape(t *testing.T) {
	for _, tc := range []struct {
		name     string
		count    int
		labels   []string
		variadic bool
		valid    bool
	}{
		{"jsonb_set", 3, nil, true, true},
		{"jsonb_insert", 4, []string{"", "", "", "insert_after"}, false, true},
		{"jsonb_set_lax", 4, []string{"jsonb_in", "path", "replacement", "null_value_treatment"}, false, true},
		{"jsonb_set", 3, []string{"replacement", "jsonb_in", "path"}, false, true},
		{"jsonb_set", 3, []string{"replacement", "replacement", "path"}, false, false},
		{"jsonb_set", 3, []string{"replacement", "jsonb_in", "unknown"}, false, false},
		{"jsonb_set", 2, nil, false, false},
		{"json_strip_nulls", 1, nil, true, true},
		{"json_strip_nulls", 1, []string{"strip_in_arrays"}, false, false},
		{"json_strip_nulls", 2, []string{"strip_in_arrays", "target"}, true, false},
		{"json_array_elements", 1, []string{"from_json"}, false, true},
		{"json_array_elements_text", 1, nil, true, true},
		{"json_array_elements", 1, []string{"target"}, false, false},
		{"json_array_elements", 2, nil, false, false},
		{"uuid_generate_v3", 2, []string{"name", "namespace"}, false, true},
		{"uuid_generate_v5", 2, []string{"namespace", "namespace"}, false, false},
		{"uuid_generate_v3", 2, nil, true, false},
		{"uuid_nil", 0, nil, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := parser.FunctionExpr{Args: make([]parser.Expr, tc.count), Variadic: tc.variadic}
			for i := range raw.Args {
				raw.Args[i] = parser.LiteralExpr{}
			}
			for _, label := range tc.labels {
				raw.ArgNames = append(raw.ArgNames, parser.Identifier{Text: label})
			}
			known, valid := StoredProviderCallShape(tc.name, raw)
			if !known || valid != tc.valid {
				t.Fatalf("known=%v valid=%v want %v", known, valid, tc.valid)
			}
		})
	}
	if known, _ := StoredProviderCallShape("abs", parser.FunctionExpr{}); known {
		t.Fatal("ordinary catalog shape claimed by a richer family")
	}
}
