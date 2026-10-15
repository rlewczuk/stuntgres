package engine

import "stuntgres/internal/parser"

func restoreSQLJSONQuery(query *parser.Select, stored bool) error {
	return parser.VisitQueryExpressions(query, func(raw parser.Expr) error {
		if parameter, ok := raw.(parser.ParameterExpr); ok {
			return expressionError("42P02", "parameters are not allowed in restore", parameter.Pos)
		}
		return nil
	})
}
