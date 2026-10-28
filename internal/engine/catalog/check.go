package catalog

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
)

// A CHECK is a typed immutable current-row expression. Its dependencies retain
// stable attribute and provider identities, never a name-keyed SQL cache.
type CheckConstraint struct {
	OID         ids.OID
	Name        string
	NoInherit   bool
	NotValid    bool
	NotEnforced bool
	// Declaration order is independent of alphabetical enforcement order.
	Ordinal            int
	Expression         expr.Expr
	Dependencies       []ExpressionDependency
	CommentSnapshot    bool
	VisibilitySnapshot bool
	// PartitionOrigin records exact immediate-parent provenance, independently
	// of ordinary inheritance topology. Nil denotes a standalone local fact.
	PartitionOrigin *InheritedConstraint
}
