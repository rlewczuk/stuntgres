package parser

import "testing"

func TestMathSignatures(t *testing.T) {
	for _, query := range []string{
		"SELECT round(2.5)", "SELECT ROUND(-2.5)", "SELECT \"round\"(1.25,1)",
		"SELECT scale(8.4100),min_scale(8.4100),trim_scale(8.4100),trunc(1.25,1),sign(-1.0),mod(9,4)",
		"SELECT round(trim_scale(8.4100),1)", "SELECT round($1,$2)",
	} {
		s, err := Parse(query)
		if err != nil || len(s) != 1 {
			t.Fatal(query, err)
		}
		selectStatement, ok := s[0].(*Select)
		if !ok || !selectStatement.IsSingleton() {
			t.Fatal(query, s)
		}
	}
	for _, query := range []string{"SELECT round(1+1)", "SELECT pg_catalog.round(1.0)", "SELECT 1"} {
		s, err := Parse(query)
		if err == nil && len(s) == 1 {
			if selected, ok := s[0].(*Select); ok && selected.IsSingleton() {
				t.Fatal("broadened singleton", query)
			}
		}
	}
	for _, query := range []string{"CREATE TABLE m(round INT,scale INT)", "SELECT round,scale FROM m"} {
		if _, err := Parse(query); err != nil {
			t.Fatal("selected name as identifier", query, err)
		}
	}
}
