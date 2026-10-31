package core_test

import (
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func stringSplitMarker(t *testing.T, b *analyze.Binder, pos int, args ...expr.Expr) functions.String {
	t.Helper()
	expr, err := b.StringNode("string_to_table", b.Type(sqltype.Text).Usage(), parser.Position{Offset: pos, Character: pos + 1}, args, [4]bool{true, true, true}, parser.OrdinaryStringSyntax)
	if err != nil {
		t.Fatal(err)
	}
	return expr.(functions.String)
}
