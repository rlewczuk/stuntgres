package parser

import (
	"strings"
	"testing"
)

func TestBinaryAccessCallAST(t *testing.T) {
	for _, name := range []string{"bit_count", "get_bit", "get_byte", "set_bit", "set_byte"} {
		args := `'\x0180'::bytea`
		if name != "bit_count" {
			args += ",0"
		}
		if strings.HasPrefix(name, "set") {
			args += ",1"
		}
		for _, spelling := range []string{name, strings.ToUpper(name)} {
			q := `/*é*/ SELECT pg_catalog."` + spelling + `"(` + args + `)`
			statements, err := Parse(q)
			if err != nil || len(statements) != 1 {
				t.Fatal(q, err)
			}
			s := statements[0].(*Select)
			call, ok := s.Targets[0].(FunctionExpr)
			if !ok || call.Name.Parts[1].Text != spelling || call.Pos.Character != 14 || call.Syntax != OrdinaryFunctionSyntax {
				t.Fatal(q, call)
			}
		}
		for _, q := range []string{"SELECT " + name + "(" + args + ")", "SELECT get_byte(set_byte(v,0,1),0) FROM t", "CREATE TABLE t(v BYTEA,CHECK(bit_count(v)>0))"} {
			if _, err := Parse(q); err != nil {
				t.Fatal(q, err)
			}
		}
	}
	for _, name := range []string{"get_bytes", "set_bits", "bit_countx", "byte_count"} {
		if binaryAccessFunction(name) {
			t.Fatal("unselected admission", name)
		}
	}
}
