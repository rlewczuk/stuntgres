package parser

import "testing"

// Syntax-only package-function units; public execution is in reference/.
func TestNumericFormattingParserAdmission(t *testing.T) {
	for _, query := range []string{
		"SELECT to_char(12::numeric,'FM99'),to_number('12','99')",
		"SELECT pg_catalog.to_number($1::text,$2::text)",
		`SELECT "to_number"('12','99')`,
		"SELECT coalesce(to_number(src,mask),0::numeric) FROM nf",
		"SELECT to_number(src,mask) FROM nf WHERE to_number(src,mask)>0::numeric ORDER BY to_number(src,mask)",
		"SELECT to_char(to_number(src,mask),'FMRN') FROM nf",
		"CREATE TABLE nf(src text,mask text,n numeric DEFAULT to_number('12','99'),CHECK(to_number(src,mask)>0::numeric))",
		"CREATE VIEW nf_view AS SELECT to_char(to_number(src,mask),'FMRN') FROM nf",
		"CREATE INDEX nf_index ON nf ((to_number(src,mask)))",
		"CREATE TABLE nf_generated(src text,n numeric GENERATED ALWAYS AS (to_number(src,'99')) STORED)",
	} {
		if statements, err := Parse(query); err != nil || len(statements) != 1 {
			t.Fatal(query, err)
		}
	}
	statements, err := Parse(`SELECT "TO_NUMBER"('12','99')`)
	if err != nil {
		t.Fatal(err)
	}
	target := statements[0].(*Select).Targets[0].(FunctionExpr)
	if target.Name.Parts[0].Text != "TO_NUMBER" {
		t.Fatal("quoted spelling canonicalized", target.Name)
	}
}
