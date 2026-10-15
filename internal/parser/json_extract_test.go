package parser

import (
	"strings"
	"testing"
)

func TestJSONExtractOperatorComposition(t *testing.T) {
	for _, op := range []string{"->", "->>", "#>", "#>>"} {
		for _, q := range []string{"SELECT doc " + op + " key FROM t", "SELECT json_extract_path(doc,'a') " + op + " key FROM t", "SELECT doc " + op + " json_extract_path_text(doc,'a') FROM t", "SELECT doc OPERATOR(pg_catalog." + op + ") key FROM t", "SELECT doc " + op + "/* comment */ key FROM t"} {
			n, ok := orderParse(t, q).Targets[0].(OperatorExpr)
			if !ok || len(n.Args) != 2 || n.Name.Parts[len(n.Name.Parts)-1].Text != op {
				t.Fatal(q, n)
			}
			if !strings.Contains(q, "OPERATOR") && n.Pos.Character != strings.Index(q, op)+1 {
				t.Fatal("operator position", q, n.Pos)
			}
		}
	}
	n := orderParse(t, `SELECT doc -> 'a' -> 0 ->> 'b' FROM t`).Targets[0].(OperatorExpr)
	middle, ok := n.Args[0].(OperatorExpr)
	if !ok || n.Name.Parts[0].Text != "->>" || middle.Name.Parts[0].Text != "->" {
		t.Fatal(n)
	}
	if _, ok := middle.Args[0].(OperatorExpr); !ok {
		t.Fatal("lost left association", middle)
	}
}
func TestJSONExtractCallAndStoredSyntax(t *testing.T) {
	for _, q := range []string{
		`SELECT json_extract_path($1,$2,$3)`,
		`SELECT json_extract_path_text(from_json => $1,VARIADIC path_elems => $2::text[])`,
		`SELECT json_extract_path($1,VARIADIC '{}'::text[])`,
	} {
		n, ok := orderParse(t, q).Targets[0].(FunctionExpr)
		if !ok || n.Pos.Character != 8 {
			t.Fatal(q, n)
		}
	}
}
