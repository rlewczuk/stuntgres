package parser

import "testing"

func TestParseCommaSourceProvenance(t *testing.T) {
	for _, tc := range []struct {
		sql   string
		comma bool
	}{
		{"SELECT a.tableoid FROM a,b", true},
		{"SELECT a.tableoid FROM a CROSS JOIN b", false},
		{"SELECT a.tableoid FROM a JOIN b ON true", false},
	} {
		statements, err := Parse(tc.sql)
		if err != nil {
			t.Fatal(err)
		}
		query := statements[0].(*Select)
		join, ok := query.From.(*JoinReference)
		if !ok || join.Comma != tc.comma || join.Kind != JoinInner {
			t.Fatalf("%s: %#v", tc.sql, join)
		}
		if join.Left.(*TableReference).Name.Parts[0].Text != "a" || join.Right.(*TableReference).Name.Parts[0].Text != "b" {
			t.Fatal("operand order changed")
		}
	}
}
