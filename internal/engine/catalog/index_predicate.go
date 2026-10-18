package catalog

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
)

// A persisted predicate keeps original dependencies even when planning removes
// an arm. Both trees use the immutable parent attribute layout.
type IndexPredicate struct {
	Definition, Execution expr.Expr
	Dependencies          []ids.ObjectRef
	Attributes            []int32
}
