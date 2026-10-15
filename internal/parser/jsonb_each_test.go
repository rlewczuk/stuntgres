package parser

import "testing"

func TestJSONBEachOrdinaryIdentifiersAndFromSyntax(t *testing.T) {
	for _, f := range []string{"jsonb_each", "jsonb_each_text"} {
		for _, q := range []string{
			"SELECT " + f + " FROM t",
			"CREATE TABLE " + f + "(" + f + " text)",
			"SELECT " + f + "(from_json => $1)",
			"SELECT " + f + "(from_json := $1)",
			"SELECT * FROM pg_catalog." + f + "($1) WITH ORDINALITY AS j(k,v,n)",
			"SELECT * FROM ROWS FROM(" + f + "($1)," + f + "($2)) AS j(k,v,k2,v2)",
			"SELECT (" + f + "($1)).value",
			"SELECT s.id,e.key,e.value FROM t s LEFT JOIN LATERAL " + f + "(s.j) e ON true",
		} {
			if _, err := Parse(q); err != nil {
				t.Fatal(q, err)
			}
		}
		if !JSONFunctionName(f) {
			t.Fatal("missing selected function", f)
		}
	}
}
