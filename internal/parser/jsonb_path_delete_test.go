package parser

import (
	"strings"
	"testing"
)

func TestJSONBPathDeleteOperatorSyntax(t *testing.T) {
	for _, q := range []string{
		"/*é*/ SELECT $1 #- $2",
		"/*é*/ SELECT $1 OPERATOR(pg_catalog.#-) $2",
		"/*é*/ SELECT $1 #-/* boundary */$2",
		"/*é*/ SELECT $1 #- -- boundary\n$2",
	} {
		statements, err := Parse(q)
		if err != nil || len(statements) != 1 {
			t.Fatal(q, err)
		}
		op, ok := statements[0].(*Select).Targets[0].(OperatorExpr)
		if !ok || len(op.Args) != 2 || op.Name.Parts[len(op.Name.Parts)-1].Text != "#-" {
			t.Fatal(q, op)
		}
		index := strings.Index(q, "#-")
		if strings.Contains(q, "OPERATOR") {
			index = strings.Index(q, "OPERATOR")
		}
		if op.Pos.Character != len([]rune(q[:index]))+1 {
			t.Fatal("operator position", q, op.Pos)
		}
		for i, arg := range op.Args {
			parameter, ok := arg.(ParameterExpr)
			if !ok || parameter.Number != i+1 {
				t.Fatal("operand order", q, arg)
			}
		}
	}
}

func TestJSONBPathDeleteComposition(t *testing.T) {
	for _, q := range []string{
		`SELECT jsonb_set('{}','{a}','1') #- '{a}'`,
		`SELECT '{"a":1}'::jsonb #- string_to_array('a', ',')`,
		`SELECT jsonb_set('{}','{a}','1') #- string_to_array('a', ',')`,
		`SELECT ('{"a":1}'::jsonb #- '{a}') -> 'b'`,
		`CREATE TABLE t(doc jsonb,path text[],v jsonb GENERATED ALWAYS AS (doc #- path) STORED,CHECK(doc #- path='{}'::jsonb))`,
		`CREATE VIEW v AS SELECT doc #- path FROM t`,
		`CREATE INDEX ix ON t ((doc #- path))`,
	} {
		if _, err := Parse(q); err != nil {
			t.Fatal(q, err)
		}
	}
	statements, err := Parse("SELECT $1 #- $2 #- $3")
	if err != nil {
		t.Fatal(err)
	}
	outer := statements[0].(*Select).Targets[0].(OperatorExpr)
	inner, ok := outer.Args[0].(OperatorExpr)
	if !ok || inner.Name.Parts[0].Text != "#-" || outer.Args[1].(ParameterExpr).Number != 3 {
		t.Fatal("not left associative", outer)
	}
	if binaryScalarFunction("jsonb_delete_path") || JSONBSetFunctionName("jsonb_delete_path") {
		t.Fatal("public function admitted")
	}
}
