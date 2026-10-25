package parser

import "testing"

func TestNamedPredicateArguments(t *testing.T) {
	for _, source := range []bool{false, true} {
		for _, qualified := range []string{"named_predicate", "public.named_predicate"} {
			query := "SELECT "+qualified+"(flag=>1=2)"
			if source { query = "SELECT * FROM "+qualified+"(flag=>1=2)" }
			statements, err := Parse(query)
			if err != nil { t.Fatal(query, err) }
			selectStmt := statements[0].(*Select)
			var call FunctionExpr
			if source { call = selectStmt.From.(*FunctionTable).Calls[0] } else { call = selectStmt.Targets[0].(FunctionExpr) }
			if len(call.Args) != 1 || len(call.ArgNames) != 1 || call.ArgNames[0].Text != "flag" { t.Fatal("named argument lost", query, call) }
			comparison, ok := call.Args[0].(OperatorExpr)
			if !ok || len(comparison.Args) != 2 || len(comparison.Name.Parts) != 1 || comparison.Name.Parts[0].Text != "=" { t.Fatal("comparison argument lost", query, call.Args[0]) }
			left, leftOK := comparison.Args[0].(LiteralExpr)
			right, rightOK := comparison.Args[1].(LiteralExpr)
			if !leftOK || !rightOK || left.Value.Integer != 1 || right.Value.Integer != 2 { t.Fatal("comparison inputs lost", query, comparison) }
		}
	}
}
