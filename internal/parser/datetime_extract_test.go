package parser

import "testing"

// AST/position units supplement the public dual-mode SQL reference contracts.
func TestDatetimeExtractSyntaxAndPositions(t *testing.T) {
	for _, q := range []string{"SELECT EXTRACT(year FROM $1::timestamp)", "SELECT EXTRACT('YeAr' FROM DATE '2000-01-01')", "SELECT EXTRACT(\"year\" FROM (DATE '2000-01-01'+1))"} {
		statements, err := Parse(q)
		if err != nil {
			t.Fatal(q, err)
		}
		call, ok := statements[0].(*Select).Targets[0].(FunctionExpr)
		if !ok || call.Syntax != SQLFunctionSyntax || len(call.Args) != 2 || call.Pos.Character != 8 || len(call.Name.Parts) != 2 || call.Name.Parts[0].Text != "pg_catalog" || call.Name.Parts[1].Text != "extract" {
			t.Fatal(q, call)
		}
		field, ok := call.Args[0].(LiteralExpr)
		if !ok || field.Value.Kind != StringValue || field.Value.Pos.Character != 16 {
			t.Fatal(q, field)
		}
	}
	for _, q := range []string{"SELECT date_part($1::text,$2::timestamp)", "SELECT pg_catalog.extract('year',DATE '2000-01-01')", "SELECT \"Date_Part\"('year',DATE '2000-01-01')", "CREATE TABLE dx(s timestamp,n numeric DEFAULT EXTRACT(year FROM TIMESTAMP '2000-01-01'),CHECK(date_part('year',s)>0))", "CREATE INDEX dx_i ON dx ((EXTRACT(year FROM s)))"} {
		if statements, err := Parse(q); err != nil || len(statements) != 1 {
			t.Fatal(q, err)
		}
	}
	statements, err := Parse("SELECT \"Date_Part\"('year',DATE '2000-01-01')")
	if err != nil {
		t.Fatal(err)
	}
	if statements[0].(*Select).Targets[0].(FunctionExpr).Name.Parts[0].Text != "Date_Part" {
		t.Fatal("quoted routine case lost")
	}
}
