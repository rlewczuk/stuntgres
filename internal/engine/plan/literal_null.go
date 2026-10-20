package plan

import (
	"stuntgres/internal/engine/expr"
)

// NullLiteral reports a NULL literal node.
func NullLiteral(node expr.Expr) bool {
	value, ok := node.(LiteralExpr)
	return ok && value.Value.IsNull()
}
