package parser

import (
	"strings"
	"testing"
)

func TestBinaryHashCallAST(t *testing.T) {
	for _, name := range []string{"crc32", "crc32c", "md5", "sha224", "sha256", "sha384", "sha512"} {
		for _, spelling := range []string{name, strings.ToUpper(name), strings.ToUpper(name[:1]) + name[1:]} {
			for _, qualified := range []bool{false, true} {
				callee := `"` + spelling + `"`
				if qualified {
					callee = "pg_catalog." + callee
				}
				q := "/*é*/ SELECT " + callee + "(VARIADIC 'abc')"
				statements, err := Parse(q)
				if err != nil || len(statements) != 1 {
					t.Fatal(q, statements, err)
				}
				s, ok := statements[0].(*Select)
				if !ok || !s.IsSingleton() || len(s.Targets) != 1 {
					t.Fatal(q, statements)
				}
				call, ok := s.Targets[0].(FunctionExpr)
				if !ok || !call.Variadic || len(call.Args) != 1 || call.Syntax != OrdinaryFunctionSyntax || call.Pos.Character != 14 || call.Pos.Offset != 14 {
					t.Fatal(q, call)
				}
				n := 1
				if qualified {
					n = 2
				}
				if len(call.Name.Parts) != n || call.Name.Parts[n-1].Text != spelling {
					t.Fatal("callee normalized", q, call)
				}
				if qualified && call.Name.Parts[0].Text != "pg_catalog" {
					t.Fatal(call)
				}
				literal, ok := call.Args[0].(LiteralExpr)
				if !ok || literal.Value.Kind != StringValue || literal.Value.Text != "abc" {
					t.Fatal(call.Args)
				}
			}
		}
		for _, q := range []string{"SELECT " + name + "(v) FROM t", "CREATE TABLE t(v BYTEA,CHECK(" + name + "(v) IS NOT NULL))", "INSERT INTO t VALUES(" + name + "('abc'))", "SELECT sha256(" + name + "(v)) FROM t"} {
			if _, err := Parse(q); err != nil {
				t.Fatal(q, err)
			}
		}
	}
	for _, name := range []string{"sha1", "sha257", "md4", "crc64", "sha256x", "ſha256"} {
		if binaryHashFunction(name) {
			t.Fatal("unselected admission", name)
		}
	}
}
