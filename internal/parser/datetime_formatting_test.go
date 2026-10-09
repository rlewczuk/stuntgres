package parser

import "testing"

// Syntax-only admission checks: these do not execute SQL or replace the shared
// external PostgreSQL/native behavior and diagnostic scenarios.
func TestDatetimeFormattingParserAdmission(t *testing.T) {
	for _, query := range []string{
		"SELECT to_char(TIMESTAMP '2006-10-19','YYYY')",
		"SELECT pg_catalog.to_char(TIMESTAMPTZ '2006-10-19 UTC','YYYY')",
		"SELECT to_char(INTERVAL '25 months','YYYY MM')",
		"SELECT to_date('2006-10-19','YYYY-MM-DD')",
		"SELECT to_timestamp('2006-10-19','YYYY-MM-DD')",
		`SELECT "to_date"('2006','YYYY')`,
		"SELECT coalesce(to_char(TIMESTAMP '2006-10-19','YYYY'),'missing')",
		"SELECT to_date(v,'YYYY') FROM fmt ORDER BY to_date(v,'YYYY')",
		"CREATE TABLE fmt(v TEXT,d DATE DEFAULT to_date('2006','YYYY'),CHECK(to_char(d,'YYYY')='2006'))",
		"CREATE VIEW fmt_view AS SELECT to_timestamp(v,'YYYY') FROM fmt",
	} {
		if statements, err := Parse(query); err != nil || len(statements) != 1 {
			t.Fatal("scalar admission", query, err)
		}
	}
	statements, err := Parse(`SELECT "TO_DATE"('2006','YYYY')`)
	if err != nil {
		t.Fatal(err)
	}
	target := statements[0].(*Select).Targets[0].(FunctionExpr)
	if len(target.Name.Parts) != 1 || target.Name.Parts[0].Text != "TO_DATE" {
		t.Fatal("quoted spelling canonicalized", target.Name)
	}
}
