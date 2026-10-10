package parser

import "testing"

func TestSizeParserAdmission(t *testing.T) {
	for _, tc := range []struct {
		call, name      string
		variadic, named bool
	}{
		{"pg_size_bytes($1)", "pg_size_bytes", false, false},
		{"pg_catalog.pg_size_pretty($1::bigint)", "pg_size_pretty", false, false},
		{"PG_SIZE_PRETTY(1::numeric)", "pg_size_pretty", false, false},
		{`"PG_SIZE_PRETTY"(1::numeric)`, "PG_SIZE_PRETTY", false, false},
		{"pg_size_bytes(VARIADIC '1 MB')", "pg_size_bytes", true, false},
		{"pg_size_pretty(value := 1024::bigint)", "pg_size_pretty", false, true},
	} {
		statements, err := Parse("/*é🙂*/ SELECT " + tc.call)
		if err != nil {
			t.Fatal(tc, err)
		}
		call, ok := statements[0].(*Select).Targets[0].(FunctionExpr)
		if !ok || call.Name.Parts[len(call.Name.Parts)-1].Text != tc.name || call.Pos.Character != 15 || call.Variadic != tc.variadic || len(call.Args) != 1 || call.StringSyntax != OrdinaryStringSyntax {
			t.Fatal(tc, call)
		}
		if tc.named && (len(call.ArgNames) != 1 || call.ArgNames[0].Text != "value") {
			t.Fatal("lost argument label", call)
		}
	}
	for _, name := range []string{"pg_size_bytes", "pg_size_pretty"} {
		statements, err := Parse("SELECT " + name + " FROM size_parser")
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := statements[0].(*Select).Targets[0].(ColumnExpr); !ok {
			t.Fatal("bare identifier became call")
		}
		if sizeFunction(name + "_other") {
			t.Fatal("unbounded admission")
		}
	}
}
