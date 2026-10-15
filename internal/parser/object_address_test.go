package parser

import (
	"context"
	"strings"
	"testing"
)

func TestObjectAddressGrammar(t *testing.T) {
	t.Run("inquiry_json_type_syntax", func(t *testing.T) {
		for _, tc := range []struct {
			input  string
			system bool
			rank   int
		}{
			{"json", true, 0}, {"JSON[]", true, 1}, {"json ARRAY", true, 1},
			{`"json"`, false, 0}, {"pg_catalog.json[]", false, 1},
		} {
			raw, err := ParseTypeName(context.Background(), tc.input, nil)
			if err != nil || raw.Name != "json" || raw.System != tc.system || len(raw.ArrayBounds) != tc.rank {
				t.Fatal(tc, raw, err)
			}
		}
	})
	for _, name := range []string{"pg_identify_object", "pg_identify_object_as_address", "pg_get_object_address", "pg_describe_object"} {
		for _, form := range []string{name, strings.ToUpper(name), `"` + name + `"`, "pg_catalog." + name} {
			for _, q := range []string{
				"SELECT " + form + "(NULL,NULL,NULL)",
				"SELECT * FROM " + form + "(NULL,NULL,NULL) WITH ORDINALITY AS a(x,y,z)",
				"SELECT * FROM ROWS FROM (" + form + "(NULL,NULL,NULL))",
				"CREATE VIEW address_parser_view AS SELECT " + form + "(NULL,NULL,NULL)",
				"CREATE INDEX address_parser_index ON t ((" + form + "(NULL,NULL,NULL)))",
				"CREATE TABLE address_parser_table(a TEXT GENERATED ALWAYS AS (" + form + "(NULL,NULL,NULL)) STORED)",
			} {
				if _, err := Parse(q); err != nil {
					t.Fatal(q, err)
				}
			}
		}
		for _, suffix := range []string{"(*)", "(DISTINCT NULL,NULL,NULL)", "(NULL,NULL,NULL ORDER BY 1)", "(NULL,NULL,NULL) FILTER(WHERE TRUE)", "(NULL,NULL,NULL) OVER()"} {
			statements, err := Parse("SELECT " + name + suffix)
			if err != nil || len(statements) != 1 {
				t.Fatal(name, suffix, err)
			}
			if _, ok := statements[0].(*Select).Targets[0].(FunctionExpr); !ok {
				t.Fatal("lost address function syntax", name, suffix)
			}
		}
	}
	for _, q := range []string{"SELECT (pg_identify_object(1259,1,0)).identity", "SELECT (pg_get_object_address(type=>'table',object_names=>'{public,t}',object_args=>'{}')).*"} {
		if _, err := Parse(q); err != nil {
			t.Fatal(q, err)
		}
	}
}
