package parser

import (
	"strings"
	"testing"
)

func TestTypeDescribeCallAdmission(t *testing.T) {
	for _, tc := range []struct {
		name, args string
		arity      int
	}{
		{"format_type", "$1,$2", 2}, {"pg_basetype", "$1", 1}, {"pg_typeof", "$1", 1},
	} {
		for _, qualified := range []bool{false, true} {
			for _, quoted := range []bool{false, true} {
				spelling, callee := tc.name, tc.name
				if quoted {
					spelling = strings.ToUpper(tc.name)
					callee = `"` + spelling + `"`
				}
				if qualified {
					callee = "pg_catalog." + callee
				}
				statements, err := Parse("SELECT " + callee + "(" + tc.args + ")")
				if err != nil || len(statements) != 1 {
					t.Fatal(callee, err)
				}
				call, ok := statements[0].(*Select).Targets[0].(FunctionExpr)
				if !ok || len(call.Args) != tc.arity || call.Name.Parts[len(call.Name.Parts)-1].Text != spelling || call.Pos.Character != 8 {
					t.Fatal(callee, call)
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
			"CREATE TABLE t(v text DEFAULT " + tc.name + "(" + tc.args + "))",
			"CREATE TABLE t(v text,CHECK(" + tc.name + "(" + tc.args + ") IS NOT NULL))",
			"INSERT INTO t VALUES(" + tc.name + "(" + tc.args + "))",
			"SELECT pg_typeof(" + tc.name + "(" + tc.args + ")) FROM t",
		} {
			if _, err := Parse(query); err != nil {
				t.Fatal(query, err)
			}
		}
	}
	for _, query := range []string{
		"SELECT pg_typeof(DISTINCT 1)", "SELECT pg_typeof(1 ORDER BY 1)",
		"SELECT pg_typeof(1) FILTER (WHERE TRUE)", "SELECT pg_typeof(1) OVER ()",
		"SELECT pg_typeof(VARIADIC 1)", "SELECT pg_typeof(*)", "SELECT pg_typeof()",
		"SELECT format_type(no_such=>23,typemod=>0)",
	} {
		if _, err := Parse(query); err != nil {
			t.Fatal(query, err)
		}
	}
}
