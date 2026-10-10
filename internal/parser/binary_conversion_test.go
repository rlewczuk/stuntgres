package parser

import (
	"strings"
	"testing"
)

func TestBinaryConversionCallAST(t *testing.T) {
	for _, tc := range []struct {
		name, args string
		arity      int
	}{
		{"convert", "$1,$2,$3", 3},
		{"convert_from", "$1,$2", 2},
		{"convert_to", "$1,$2", 2},
		{"encode", "$1,$2", 2},
		{"decode", "$1,$2", 2},
	} {
		for _, quoted := range []bool{false, true} {
			for _, qualified := range []bool{false, true} {
				spelling := tc.name
				callee := spelling
				if quoted {
					spelling = strings.ToUpper(tc.name)
					callee = `"` + spelling + `"`
				}
				if qualified {
					callee = "pg_catalog." + callee
				}
				query := "SELECT " + callee + "(" + tc.args + ")"
				statements, err := Parse(query)
				if err != nil || len(statements) != 1 {
					t.Fatal(query, statements, err)
				}
				selectNode, ok := statements[0].(*Select)
				if !ok || len(selectNode.Targets) != 1 {
					t.Fatal(query, statements)
				}
				call, ok := selectNode.Targets[0].(FunctionExpr)
				if !ok || len(call.Args) != tc.arity || call.Syntax != OrdinaryFunctionSyntax {
					t.Fatal(query, selectNode.Targets)
				}
				last := len(call.Name.Parts) - 1
				if call.Name.Parts[last].Text != spelling {
					t.Fatal("quoted spelling changed", call)
				}
				for i, arg := range call.Args {
					parameter, ok := arg.(ParameterExpr)
					if !ok || parameter.Number != i+1 {
						t.Fatal(call.Args)
					}
				}
			}
		}
		for _, query := range []string{
			"SELECT " + tc.name + "(" + tc.args + ") FROM t",
			"CREATE TABLE t(v bytea,CHECK(" + tc.name + "(" + tc.args + ") IS NOT NULL))",
			"INSERT INTO t VALUES(" + tc.name + "(" + tc.args + "))",
			"SELECT encode(" + tc.name + "(" + tc.args + "),'hex') FROM t",
		} {
			if _, err := Parse(query); err != nil {
				t.Fatal(query, err)
			}
		}
	}
}
