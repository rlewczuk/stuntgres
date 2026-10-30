package index

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

// ExpressionTransform is a method-independent analyzed expression for this
// CREATE operation only. It separates parse transformation from later physical
// definition checks without retaining a binder or binding the expression twice.
// Complete consumes its input/dependency/column-use facts; only the resulting
// stored definition and execution recipe may enter the catalog.
type ExpressionTransform struct {
	Definition   expr.Expr
	Input        sqltype.InputEnvironment
	Dependencies []catalog.ExpressionDependency
	Uses         []int
}
