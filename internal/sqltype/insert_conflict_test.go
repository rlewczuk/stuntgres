package sqltype

import "testing"

func TestInsertConflictClassInferenceIdentity(t *testing.T) {
	for _, c := range []struct {
		left, right string
		want        bool
	}{
		{"text_ops", "varchar_ops", true}, {"text_pattern_ops", "varchar_pattern_ops", true},
		{"int4_ops", "int4_ops", true}, {"int2_ops", "int4_ops", false}, {"int4_ops", "int8_ops", false},
		{"float4_ops", "float8_ops", false}, {"date_ops", "timestamp_ops", false},
		{"text_ops", "text_pattern_ops", false}, {"text_ops", "bpchar_ops", false},
	} {
		left, ok := LookupIndexClass(c.left)
		if !ok {
			t.Fatal(c.left)
		}
		right, ok := LookupIndexClass(c.right)
		if !ok {
			t.Fatal(c.right)
		}
		if got := left.SameInferenceIdentity(right); got != c.want {
			t.Fatal(c, got)
		}
		if got := right.SameInferenceIdentity(left); got != c.want {
			t.Fatal("asymmetric identity", c, got)
		}
	}
}
