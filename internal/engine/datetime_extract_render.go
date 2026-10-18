package engine

import (
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
)

// Logical replay uses the existing SQL literal encoder for the field. Public
// PG deparse intentionally uses raw field text instead, matching ruleutils.
func datetimeExtractLogicalSQL(e functions.DatetimeExtract[BoundExpr, ExprContext, ExprProperties], ordinary string, child func(BoundExpr) (string, error)) (string, error) {
	field, err := child(e.Args[0])
	if err != nil {
		return "", err
	}
	source, err := child(e.Args[1])
	if err != nil {
		return "", err
	}
	if e.Syntax == parser.SQLFunctionSyntax {
		return "EXTRACT(" + field + " FROM " + source + ")", nil
	}
	return ordinary + "(" + field + ", " + source + ")", nil
}
