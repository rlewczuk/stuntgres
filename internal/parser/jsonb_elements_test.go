package parser

import "testing"

func TestJSONBElementsOrdinaryIdentifiersAndFromSyntax(t *testing.T) {
	for _, f := range []string{"jsonb_array_elements", "jsonb_array_elements_text"} {
		for _, q := range []string{
			"SELECT " + f + " FROM jbe_source",
			"SELECT " + f + "(from_json => $1)",
			"SELECT " + f + "(from_json := $1)",
			"SELECT * FROM pg_catalog." + f + "($1) WITH ORDINALITY AS j(v,n)",
			"SELECT * FROM ROWS FROM (" + f + "($1)," + f + "($2)) AS j(a,b)",
			"SELECT s.id,e.value FROM jbe_source s LEFT JOIN LATERAL " + f + "(s.j) e ON true",
			"CREATE VIEW jbe_v AS SELECT " + f + "(j) FROM jbe_source",
		} {
			if _, err := Parse(q); err != nil {
				t.Fatal(q, err)
			}
		}
		if !JSONFunctionName(f) {
			t.Fatal("missing function admission", f)
		}
	}
	for _, f := range []string{"jsonb_each", "jsonb_each_text"} {
		if JSONFunctionName(f) {
			t.Fatal("adjacent capability admitted", f)
		}
	}
}
