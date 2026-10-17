package parser

import (
	"strings"
	"testing"
)

func TestMathHyperbolicAdmission(t *testing.T) {
	for _, query := range []string{
		"SELECT sinh(1),cosh(0),tanh(1),asinh(1),acosh(1),atanh('0.5')",
		"SELECT sinh,cosh,tanh,asinh,acosh,atanh FROM t",
		"SELECT sinh(sinh),acosh(acosh),atanh(tanh(d)) FROM t",
		"INSERT INTO t SELECT id,acosh(d) FROM source ORDER BY id",
		"UPDATE t SET d=asinh(d)",
		"CREATE VIEW v AS SELECT sinh(d),cosh(d),tanh(d),asinh(d),acosh(d),atanh(d) FROM t",
	} {
		statements, err := Parse(query)
		if err != nil || len(statements) != 1 {
			t.Fatal(query, statements, err)
		}
	}
}

func TestMathHyperbolicProvenance(t *testing.T) {
	for _, name := range []string{"sinh", "cosh", "tanh", "asinh", "acosh", "atanh"} {
		for _, spelling := range []string{name, strings.ToUpper(name)} {
			statements, err := Parse(`/*é*/ SELECT pg_catalog."` + spelling + `"($1)`)
			if err != nil || len(statements) != 1 {
				t.Fatal(statements, err)
			}
			call, ok := statements[0].(*Select).Targets[0].(FunctionExpr)
			if !ok || call.Pos.Character != 14 || call.Pos.Offset != 14 || len(call.Name.Parts) != 2 || call.Name.Parts[1].Text != spelling || len(call.Args) != 1 {
				t.Fatal("quoted spelling and source provenance", call)
			}
			parameter, ok := call.Args[0].(ParameterExpr)
			if !ok || parameter.Number != 1 {
				t.Fatal("parameter provenance", call.Args)
			}
		}
	}
}

func TestMathHyperbolicBoundaries(t *testing.T) {
	for _, query := range []string{
		"INSERT INTO t VALUES(sinh(1))",
		"CREATE TABLE t(d DOUBLE PRECISION DEFAULT cosh(0))",
		"SELECT asinh(1,)",
		"SELECT acosh(1) garbage garbage",
		"INSERT INTO t VALUES(1); SELECT atanh(1,)",
	} {
		statements, err := Parse(query)
		if err == nil || len(statements) != 0 {
			t.Fatal("widened or partial grammar", query, statements, err)
		}
	}
}
