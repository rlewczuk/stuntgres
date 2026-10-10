package engine

import "stuntgres/internal/parser"

// Logical replay uses the existing SQL literal encoder for the field. Public
// PG deparse intentionally uses raw field text instead, matching ruleutils.
func (e datetimeExtractExpression) logicalSQL(ordinary string, child func(BoundExpr) (string, error)) (string, error) {
	field, err := child(e.args[0])
	if err != nil {
		return "", err
	}
	source, err := child(e.args[1])
	if err != nil {
		return "", err
	}
	if e.syntax == parser.SQLFunctionSyntax {
		return "EXTRACT(" + field + " FROM " + source + ")", nil
	}
	return ordinary + "(" + field + ", " + source + ")", nil
}
