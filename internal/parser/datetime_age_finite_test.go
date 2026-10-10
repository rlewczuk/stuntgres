package parser

import "testing"

// Syntax-only units; public execution/diagnostics remain in reference/.
func TestDatetimeAgeFiniteParserAdmission(t *testing.T) {
	for _, q := range []string{
		"SELECT age(TIMESTAMP '2004-06-01',TIMESTAMP '2004-04-30')",
		"SELECT pg_catalog.age($1::timestamp)",
		"SELECT isfinite(DATE 'infinity'),isfinite(INTERVAL '-infinity')",
		"SELECT CASE WHEN isfinite(t) THEN age(t) ELSE NULL::interval END FROM af",
		"CREATE TABLE af(t timestamp,span interval DEFAULT age(CURRENT_DATE::timestamp),CHECK(isfinite(t)))",
		"CREATE VIEW af_view AS SELECT age(t,TIMESTAMP '2000-01-01') FROM af",
		"CREATE INDEX af_index ON af ((isfinite(t)))",
	} {
		if statements, err := Parse(q); err != nil || len(statements) != 1 {
			t.Fatal(q, err)
		}
	}
	statements, err := Parse(`SELECT "AGE"(TIMESTAMP '2000-01-01')`)
	if err != nil {
		t.Fatal(err)
	}
	target := statements[0].(*Select).Targets[0].(FunctionExpr)
	if target.Name.Parts[0].Text != "AGE" {
		t.Fatal("quoted case lost", target.Name)
	}
}
