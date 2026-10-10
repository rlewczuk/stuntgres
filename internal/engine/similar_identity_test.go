package engine

import (
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
		columnExpression{expressionInfo: expressionInfo{usage: usage}, scope: 1, frame: 1, slot: 0},
		literalExpression{expressionInfo: expressionInfo{usage: usage}, value: sqltype.String(text, "%")},
		literalExpression{expressionInfo: expressionInfo{usage: usage}, value: sqltype.String(text, "#")},
	}
	ordinary := similarSubstringExpression{expressionInfo: expressionInfo{usage: usage}, args: args, provider: provider, syntax: parser.OrdinaryFunctionSyntax}
	standard := ordinary
	standard.syntax = parser.SQLFunctionSyntax
	if !sameGroupExpression(ordinary, standard) || !sameGroupExpression(standard, ordinary) {
		t.Fatal("presentation syntax changed semantic grouped identity")
	}
	different := standard
	different.args[1] = literalExpression{expressionInfo: expressionInfo{usage: usage}, value: sqltype.String(text, "a%")}
	if sameGroupExpression(ordinary, different) {
		t.Fatal("different patterns compared equal")
	}
	different = standard
	different.args[2] = literalExpression{expressionInfo: expressionInfo{usage: usage}, value: sqltype.String(text, "!")}
	if sameGroupExpression(ordinary, different) {
		t.Fatal("different escapes compared equal")
	}
	different = standard
	different.args[0] = columnExpression{expressionInfo: expressionInfo{usage: usage}, scope: 1, frame: 1, slot: 1}
	if sameGroupExpression(ordinary, different) {
		t.Fatal("different source children compared equal")
	}
	different = standard
	different.provider, err = sqltype.BindSimilarSubstring([3]sqltype.Usage{usage, usage, usage}, usage, false)
	if err != nil {
		t.Fatal(err)
	}
	if sameGroupExpression(ordinary, different) {
		t.Fatal("different provider descriptors compared equal")
	}
}
