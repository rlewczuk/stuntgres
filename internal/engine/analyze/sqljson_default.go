package analyze

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
)

// ValidateSQLJSONDefault inspects the ordinary analysis result before RETURNING
// fallback conversion. Enclosing shape precedes the full-tree column restriction;
// parameter descendants of an accepted operator/function are permitted.
func (b *Binder) ValidateSQLJSONDefault(raw parser.Expr, bound expr.Expr) error {
	if !sqlJSONDefaultExpression(raw, bound) {
		return expr.ErrorAt("42804", "can only specify a constant, non-aggregate function, or operator expression for DEFAULT", sqlJSONDefaultPosition(raw, bound))
	}
	hasColumn := false
	WalkQueryArgumentExpression(bound, func(node expr.Expr) {
		if _, ok := node.(plan.ColumnExpr); ok {
			hasColumn = true
		}
	})
	if hasColumn {
		return expr.ErrorAt("42804", "DEFAULT expression must not contain column references", sqlJSONDefaultPosition(raw, bound))
	}
	return nil
}

func sqlJSONDefaultPosition(raw parser.Expr, bound expr.Expr) parser.Position {
	// Transparent analyzed wrappers retain their operand's location, including
	// specialized regclass syntax whose raw position is the cast separator.
	for {
		switch node := bound.(type) {
		case plan.CoercionExpr:
			bound = node.Child
		case plan.CollationExpr:
			bound = node.Child
		default:
			goto unwrapped
		}
	}
unwrapped:
	raw = sqlJSONDefaultOperand(raw)
	if _, ok := raw.(parser.OperatorExpr); ok {
		return parser.ExpressionStart(raw)
	}
	return bound.SourcePosition()
}

func sqlJSONDefaultOperand(raw parser.Expr) parser.Expr {
	for {
		switch node := raw.(type) {
		case parser.CastExpr:
			raw = node.Child
		case parser.RegclassExpr:
			raw = node.Child
		case parser.CollateExpr:
			raw = node.Child
		default:
			return raw
		}
	}
}

func sqlJSONDefaultExpression(raw parser.Expr, bound expr.Expr) bool {
	for {
		switch node := bound.(type) {
		case plan.CoercionExpr:
			if node.Plan.FunctionExpression() {
				return true
			}
			bound = node.Child
		case plan.CollationExpr:
			bound = node.Child
		default:
			goto unwrapped
		}
	}
unwrapped:
	switch bound.(type) {
	case plan.LiteralExpr:
		return true
	case plan.ParameterExpr:
		return false
	}
	// No-op casts may disappear during analysis. Their written presence cannot
	// legalize a Param or special construct, unlike an actual cast function.
	switch sqlJSONDefaultOperand(raw).(type) {
	case parser.LiteralExpr, parser.TypedLiteralExpr, parser.OperatorExpr, parser.CallExpr, parser.FunctionExpr, parser.TimeZoneExpr:
		return true
	}
	return false
}
