package parser

import "testing"

func TestCreateTemporaryViewSyntax(t *testing.T) {
	for _, q := range []string{
		"CREATE TEMP VIEW v AS SELECT 11 AS v",
		"CREATE TEMPORARY VIEW pg_temp.v(k) WITH(security_invoker=true) AS SELECT id FROM t",
		"CREATE OR REPLACE TEMP VIEW v AS SELECT id FROM t",
		"CREATE OR REPLACE TEMPORARY VIEW v AS SELECT id FROM t",
	} {
		t.Run(q, func(t *testing.T) {
			statements, err := Parse(q)
			if err != nil || len(statements) != 1 {
				t.Fatal(statements, err)
			}
			view, ok := statements[0].(*CreateView)
			if !ok || view.Query == nil || len(view.Name.Parts) == 0 || view.Persistence != "temporary" {
				t.Fatal(statements)
			}
		})
	}
	for _, q := range []string{
		"CREATE TEMP VIEW v AS SELECT 11 AS v; SELECT FROM",
		"CREATE TEMP VIEW v AS SELECT 11 AS v garbage extra",
		`CREATE TEMP VIEW "é" AS SELECT 11 AS v; @`,
	} {
		if statements, err := Parse(q); err == nil || len(statements) != 0 {
			t.Fatal(q, statements, err)
		}
	}
}
