package catalog

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
)

// Own recipes are immutable and alphabetically ordered. Ancestors remain on
// their own descriptors; conversion preparation and evaluation traverse them first.
type DomainCheckConstraint struct {
	OID          ids.OID
	Name         string
	NotValid     bool
	Ordinal      int
	Expression   expr.Expr
	Dependencies []ExpressionDependency
}
