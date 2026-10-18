package engine

import (
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestSimilarSubstringPresentationIdentity(t *testing.T) {
	text, _ := similarUnitTypes()
	usage := text.Usage()
	provider, err := sqltype.BindSimilarSubstring([3]sqltype.Usage{usage, usage, usage}, usage, true)
	if err != nil {
		t.Fatal(err)
	}
	args := [3]BoundExpr{
		columnExpression{Info: expressionInfo{Usage: usage}, scope: 1, frame: 1, slot: 0},
		literalExpression{Info: expressionInfo{Usage: usage}, value: sqltype.String(text, "%")},
		literalExpression{Info: expressionInfo{Usage: usage}, value: sqltype.String(text, "#")},
	}
	ordinary := functions.SimilarSubstring{Info: expressionInfo{Usage: usage}, Args: args, Provider: provider, Syntax: parser.OrdinaryFunctionSyntax}
	standard := ordinary
	standard.Syntax = parser.SQLFunctionSyntax
	if !sameGroupExpression(ordinary, standard) || !sameGroupExpression(standard, ordinary) {
		t.Fatal("presentation syntax changed semantic grouped identity")
	}
	different := standard
	different.Args[1] = literalExpression{Info: expressionInfo{Usage: usage}, value: sqltype.String(text, "a%")}
	if sameGroupExpression(ordinary, different) {
		t.Fatal("different patterns compared equal")
	}
	different = standard
	different.Args[2] = literalExpression{Info: expressionInfo{Usage: usage}, value: sqltype.String(text, "!")}
	if sameGroupExpression(ordinary, different) {
		t.Fatal("different escapes compared equal")
	}
	different = standard
	different.Args[0] = columnExpression{Info: expressionInfo{Usage: usage}, scope: 1, frame: 1, slot: 1}
	if sameGroupExpression(ordinary, different) {
		t.Fatal("different source children compared equal")
	}
	different = standard
	different.Provider, err = sqltype.BindSimilarSubstring([3]sqltype.Usage{usage, usage, usage}, usage, false)
	if err != nil {
		t.Fatal(err)
	}
	if sameGroupExpression(ordinary, different) {
		t.Fatal("different provider descriptors compared equal")
	}
}
