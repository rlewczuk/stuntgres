package engine

import (
	"stuntgres/internal/engine/expr"
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
	args := [3]expr.Expr{
		columnExpression{Info: expr.Info{Usage: usage}, Scope: 1, Frame: 1, Slot: 0},
		literalExpression{Info: expr.Info{Usage: usage}, Value: sqltype.String(text, "%")},
		literalExpression{Info: expr.Info{Usage: usage}, Value: sqltype.String(text, "#")},
	}
	ordinary := functions.SimilarSubstring{Info: expr.Info{Usage: usage}, Args: args, Provider: provider, Syntax: parser.OrdinaryFunctionSyntax}
	standard := ordinary
	standard.Syntax = parser.SQLFunctionSyntax
	if !sameGroupExpression(ordinary, standard) || !sameGroupExpression(standard, ordinary) {
		t.Fatal("presentation syntax changed semantic grouped identity")
	}
	different := standard
	different.Args[1] = literalExpression{Info: expr.Info{Usage: usage}, Value: sqltype.String(text, "a%")}
	if sameGroupExpression(ordinary, different) {
		t.Fatal("different patterns compared equal")
	}
	different = standard
	different.Args[2] = literalExpression{Info: expr.Info{Usage: usage}, Value: sqltype.String(text, "!")}
	if sameGroupExpression(ordinary, different) {
		t.Fatal("different escapes compared equal")
	}
	different = standard
	different.Args[0] = columnExpression{Info: expr.Info{Usage: usage}, Scope: 1, Frame: 1, Slot: 1}
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
