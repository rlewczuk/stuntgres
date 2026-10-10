package parser

import (
	"strings"
	"testing"
)

// Syntax-only admission witnesses; public execution remains in reference/.
func TestDatetimeJustifyParserAdmission(t *testing.T) {
	for _, name := range []string{"justify_days", "justify_hours", "justify_interval"} {
		for _, q := range []string{
			"SELECT " + name + "(INTERVAL '30 days')",
			"SELECT pg_catalog." + name + "($1)",
			"CREATE TABLE j(span interval DEFAULT " + name + "(INTERVAL '30 days'))",
			"CREATE VIEW j_view AS SELECT " + name + "(span) FROM j",
			"CREATE INDEX j_index ON j ((" + name + "(span)))",
		} {
			if statements, err := Parse(q); err != nil || len(statements) != 1 {
				t.Fatal(q, err)
			}
		}
		upper := strings.ToUpper(name)
		statements, err := Parse(`SELECT "` + upper + `"(INTERVAL '30 days')`)
		if err != nil {
			t.Fatal(err)
		}
		target := statements[0].(*Select).Targets[0].(FunctionExpr)
		if target.Name.Parts[0].Text != upper {
			t.Fatal("quoted spelling lost", target.Name)
		}
	}
}
