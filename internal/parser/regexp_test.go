package parser

import "testing"

func TestRegexpFunctionSyntax(t *testing.T) {
	for _, name := range []string{"regexp_count", "regexp_instr", "regexp_substr", "regexp_like", "regexp_match", "regexp_matches", "regexp_replace", "regexp_split_to_array", "regexp_split_to_table"} {
		for _, prefix := range []string{"", "pg_catalog."} {
			node, ok := orderParse(t, "SELECT "+prefix+name+"(string=>$1,pattern=>'a')").Targets[0].(FunctionExpr)
			if !ok || len(node.Args) != 2 || len(node.ArgNames) != 2 || node.ArgNames[0].Text != "string" || node.ArgNames[1].Text != "pattern" || node.Name.Parts[len(node.Name.Parts)-1].Text != name {
				t.Fatal(name, prefix, node)
			}
		}
	}
	node := orderParse(t, `SELECT regexp_instr('a','a',1,"N"=>2)`).Targets[0].(FunctionExpr)
	if node.ArgNames[3].Text != "N" {
		t.Fatal(node)
	}
	if !ContainsStringExpression(node) {
		t.Fatal("INSERT analysis omitted regexp call")
	}
}
func TestRegexpFromSyntax(t *testing.T) {
	for _, q := range []string{
		`SELECT r.* FROM regexp_matches('aa','a','g') WITH ORDINALITY r(m,o)`,
		`SELECT r.* FROM LATERAL pg_catalog.regexp_matches('aa','a','g') WITH ORDINALITY r(m,o)`,
		`SELECT r.* FROM ROWS FROM(regexp_matches('aa','a','g'),regexp_split_to_table('x','z')) WITH ORDINALITY r(m,s,o)`,
	} {
		selectNode := orderParse(t, q)
		alias, ok := selectNode.From.(*AliasReference)
		if !ok {
			t.Fatal(q, selectNode.From)
		}
		source, ok := alias.Input.(*FunctionFrom)
		if !ok || !source.Ordinality || len(source.Calls) == 0 || source.Calls[0].Name.Parts[len(source.Calls[0].Name.Parts)-1].Text != "regexp_matches" {
			t.Fatal(q, source)
		}
		if q == `SELECT r.* FROM LATERAL pg_catalog.regexp_matches('aa','a','g') WITH ORDINALITY r(m,o)` && !source.Lateral {
			t.Fatal(source)
		}
		if len(source.Calls) == 2 && len(alias.Columns) != 3 {
			t.Fatal(alias)
		}
	}
	node := orderParse(t, `SELECT regexp_matches,ordinality FROM regexp_matches('a','a') WITH ORDINALITY`).From.(*FunctionFrom)
	if len(node.Calls) != 1 {
		t.Fatal(node)
	}
}
