package parser

import (
	"strings"
	"testing"
)

func TestMetaphoneParserAdmission(t *testing.T) {
	for _, tc := range []struct {
		name, body, written string
		arity               int
	}{
		{"metaphone", "'a',VARIADIC ARRAY[4]", "metaphone", 2},
		{"dmetaphone", "VARIADIC ARRAY['a']", "dmetaphone", 1},
		{"dmetaphone_alt", "VARIADIC ARRAY['a']", "dmetaphone_alt", 1},
		{`"Metaphone"`, "'a',VARIADIC ARRAY[4]", "Metaphone", 2},
	} {
		for _, prefix := range []string{"", "phon_addon."} {
			q := "SELECT " + prefix + tc.name + "(" + tc.body + ")"
			t.Run(q, func(t *testing.T) {
				statements, err := Parse(q)
				if err != nil || len(statements) != 1 {
					t.Fatal(statements, err)
				}
				call, ok := statements[0].(*Select).Targets[0].(FunctionExpr)
				if !ok || call.Syntax != OrdinaryFunctionSyntax || !call.Variadic || call.ProjectionOnly || len(call.Args) != tc.arity || len(call.ArgNames) != 0 || call.Pos.Character != 8 {
					t.Fatal("lost scalar/variadic call shape", call)
				}
				parts := call.Name.Parts
				wantParts := 1
				if prefix != "" {
					wantParts = 2
				}
				if len(parts) != wantParts || parts[len(parts)-1].Text != tc.written || (prefix != "" && parts[0].Text != "phon_addon") {
					t.Fatal("lost written qualified identity", parts)
				}
				array, ok := call.Args[len(call.Args)-1].(ArrayExpr)
				if !ok || len(array.Elements) != 1 || array.Pos.Character != strings.Index(q, "ARRAY")+1 {
					t.Fatal("lost VARIADIC array argument", call.Args)
				}
				if tc.arity == 2 {
					literal, ok := call.Args[0].(LiteralExpr)
					if !ok || literal.Value.Kind != StringValue || literal.Value.Text != "a" || literal.SourcePosition().Character != strings.Index(q, "'a'")+1 {
						t.Fatal("lost positional prefix", call.Args)
					}
				}
			})
		}
	}
	// Calls remain scalars in nonprojection contexts. Bare names remain ordinary
	// identifiers; admission does not install a function or accept a signature.
	for _, q := range []string{
		"SELECT metaphone,dmetaphone,dmetaphone_alt FROM phon_source",
		"SELECT 1 AS metaphone",
		"SELECT 1 FROM phon_source WHERE metaphone(name,4)='KM'",
		"SELECT dmetaphone('Smith') || dmetaphone_alt('Smith')",
		"CREATE VIEW phon_literals AS SELECT phon_addon.metaphone('AX',2)",
	} {
		if _, err := Parse(q); err != nil {
			t.Fatal(q, err)
		}
	}
}
