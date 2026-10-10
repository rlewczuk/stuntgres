package parser

import "testing"

func TestStringNumericBaseParserAdmission(t *testing.T) {
	for _, tc := range []struct {
		call, name string
		variadic   bool
	}{
		{"to_bin($1::integer)", "to_bin", false},
		{"pg_catalog.to_oct($1::bigint)", "to_oct", false},
		{"TO_HEX(1)", "to_hex", false},
		{`"TO_HEX"(1)`, "TO_HEX", false},
		{"to_hex(VARIADIC 1)", "to_hex", true},
		{"md5(VARIADIC 'abc')", "md5", true},
		{"to_hex(value := 1)", "to_hex", false},
	} {
		statements, err := Parse("/*é🙂*/ SELECT " + tc.call)
		if err != nil {
			t.Fatal(tc, err)
		}
		selected := statements[0].(*Select)
		call, ok := selected.Targets[0].(FunctionExpr)
		if !ok || call.Name.Parts[len(call.Name.Parts)-1].Text != tc.name || call.Pos.Character != 15 || call.Variadic != tc.variadic || len(call.Args) != 1 || call.StringSyntax != OrdinaryStringSyntax {
			t.Fatal(tc, call)
		}
		if tc.call == "to_hex(value := 1)" && (len(call.ArgNames) != 1 || call.ArgNames[0].Text != "value") {
			t.Fatal("lost argument label", call)
		}
	}
	for _, name := range []string{"to_bin", "to_oct", "to_hex"} {
		statements, err := Parse("SELECT " + name + " FROM snb_parser")
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := statements[0].(*Select).Targets[0].(ColumnExpr); !ok {
			t.Fatal("bare identifier became call")
		}
		if stringNumericBaseFunction(name + "_other") {
			t.Fatal("unbounded admission")
		}
	}
}
