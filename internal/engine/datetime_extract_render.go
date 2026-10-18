package engine

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
)

// Logical replay uses the existing SQL literal encoder for the field. Public
// PG deparse instead quotes the field as an identifier, matching ruleutils.
func datetimeExtractLogicalSQL(e functions.DatetimeExtract, ordinary string, child func(expr.Expr) (string, error)) (string, error) {
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
