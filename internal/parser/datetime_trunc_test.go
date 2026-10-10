package parser

import "testing"

// Syntax only; public execution witnesses live in reference/.
func TestDatetimeTruncParserAdmission(t *testing.T) {
	for _, q := range []string{
		"SELECT date_trunc('day',TIMESTAMP '2001-01-01')",
		"SELECT pg_catalog.date_trunc($1,$2,$3)",
		"CREATE TABLE dt(v timestamp DEFAULT date_trunc('week',TIMESTAMP '2006-01-01'))",
		"CREATE VIEW dt_v AS SELECT date_trunc('hour',v) FROM dt",
		"CREATE INDEX dt_i ON dt ((date_trunc('hour',v)))",
	} {
		if statements, err := Parse(q); err != nil || len(statements) != 1 {
			t.Fatal(q, err)
		}
	}
	statements, err := Parse(`SELECT "Date_Trunc"('day',TIMESTAMP '2001-01-01')`)
	if err != nil {
		t.Fatal(err)
	}
	target := statements[0].(*Select).Targets[0].(FunctionExpr)
	if target.Name.Parts[0].Text != "Date_Trunc" || len(target.Args) != 2 {
		t.Fatal(target)
	}
	if _, err := Parse("SELECT date_trunc('day',TIMESTAMP '2001-01-01') unexpected suffix"); err == nil {
		t.Fatal("trailing suffix admitted")
	}
}
