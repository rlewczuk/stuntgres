package parser

import (
	"strings"
	"testing"
)

func TestUUIDExtractParserAdmission(t *testing.T) {
	for _, name := range []string{"uuid_extract_timestamp", "uuid_extract_version"} {
		for _, q := range []string{
			"SELECT " + name + "(v) FROM r",
			"SELECT pg_catalog." + name + "(v) FROM r",
			"SELECT " + name + "(DISTINCT v) FROM r",
			"SELECT " + name + "(v ORDER BY k) FROM r",
			"SELECT " + name + "(v) FILTER (WHERE true) FROM r",
			"SELECT " + name + "(v) OVER () FROM r",
			"SELECT " + name + "(x => v) FROM r",
		} {
			statements, err := Parse(q)
			if err != nil || len(statements) != 1 {
				t.Fatal(q, err)
			}
			call, ok := statements[0].(*Select).Targets[0].(FunctionExpr)
			if !ok || call.Name.Parts[len(call.Name.Parts)-1].Text != name {
				t.Fatal("function identity", q)
			}
		}
		statements, err := Parse(`SELECT "` + strings.ToUpper(name) + `"(v) FROM r`)
		if err != nil {
			t.Fatal(err)
		}
		if statements[0].(*Select).Targets[0].(FunctionExpr).Name.Parts[0].Text != strings.ToUpper(name) {
			t.Fatal("quoted spelling lost")
		}
		for _, q := range []string{"SELECT " + name + " FROM r", "CREATE TABLE r(" + name + " integer)", "SELECT " + name + "(NULL)"} {
			if _, err := Parse(q); err != nil {
				t.Fatal(q, err)
			}
		}
	}
	if _, err := Parse("CREATE TABLE r(v uuid,g smallint GENERATED ALWAYS AS (uuid_extract_version(v)) STORED,CHECK (uuid_extract_version(v)<>5))"); err != nil {
		t.Fatal(err)
	}
}
