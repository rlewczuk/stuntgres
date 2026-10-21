package plan

import (
	"stuntgres/internal/engine/expr"
)

// StructuralNull only recognizes transparent coercions of a literal NULL.
// Other expressions remain definitions even when evaluation would return NULL.
func StructuralNull(node expr.Expr) bool {
	for {
		switch n := node.(type) {
		case LiteralExpr:
			return n.Value.IsNull()
		case CollationExpr:
			node = n.Child
		case CoercionExpr:
			node = n.Child
		default:
			return false
		}
	}
}
