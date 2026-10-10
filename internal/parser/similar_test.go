package parser

import "testing"

func TestSimilarLoweringProvenance(t *testing.T) {
	for _, negate := range []bool{false, true} {
		query := `SELECT v SIMILAR TO p ESCAPE e FROM t`
		want := "~"
		if negate {
			query = `SELECT v NOT SIMILAR TO p ESCAPE e FROM t`
			want = "!~"
		}
		selectStatement := orderParse(t, query)
		node, ok := selectStatement.Targets[0].(OperatorExpr)
		if !ok || !node.SQLSimilar || node.Name.Parts[0].Text != want || node.Pos.Character != 10 {
			t.Fatal(node)
		}
		normalizer, ok := node.Args[1].(FunctionExpr)
		if !ok || normalizer.Syntax != SimilarEscapeFunctionSyntax || len(normalizer.Args) != 2 || normalizer.Name.Parts[1].Text != "similar_to_escape" {
			t.Fatal(normalizer)
		}
	}
}

func TestSimilarEscapeOmissionAndSubstring(t *testing.T) {
	node := orderParse(t, `SELECT v SIMILAR TO p FROM t`).Targets[0].(OperatorExpr)
	if len(node.Args[1].(FunctionExpr).Args) != 1 {
		t.Fatal(node)
	}
	for _, query := range []string{`SELECT substring(v SIMILAR p ESCAPE e) FROM t`, `SELECT substring(v FROM p FOR e) FROM t`, `SELECT substring(v,p,e) FROM t`} {
		fn := orderParse(t, query).Targets[0].(FunctionExpr)
		if len(fn.Args) != 3 || fn.Name.Parts[len(fn.Name.Parts)-1].Text != "substring" {
			t.Fatal(fn)
		}
	}
	for _, query := range []string{`SELECT v SIMILAR p FROM t`, `SELECT v SIMILAR TO FROM t`, `SELECT substring(v SIMILAR p) FROM t`} {
		if _, err := Parse(query); err == nil {
			t.Fatal("incomplete SQL regex accepted", query)
		}
	}
}

func TestSimilarPostfixCastContinuation(t *testing.T) {
	for _, query := range []string{`SELECT 'a'::bytea SIMILAR TO 'a'`, `SELECT 'a'::bytea NOT SIMILAR TO 'a'`, `SELECT 'a'::varchar SIMILAR TO 'a'`} {
		node, ok := orderParse(t, query).Targets[0].(OperatorExpr)
		if !ok || !node.SQLSimilar {
			t.Fatal("postfix cast consumed SQL-regex continuation", node)
		}
		if _, ok := node.Args[0].(CastExpr); !ok {
			t.Fatal("lost source cast", node.Args[0])
		}
	}
}
