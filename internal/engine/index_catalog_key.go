package engine

import (
	"fmt"

	"stuntgres/internal/engine/expr"
)

// PostgreSQL ComputeIndexAttrs strips top-level CollateExpr before deciding
// whether a key is an attribute or an indexprs entry. Do not strip coercions or
// use execution simplification: native execution/original trees stay untouched.
// Both catalog projection and compact index deparsing use this one decision.
func indexCatalogKey(parent *relationDescriptor, key indexKey) (int32, expr.Expr, error) {
	if key.Derived == nil {
		if !parent.hasAttribute(key.Attribute) {
			return 0, nil, fmt.Errorf("stuntgres: index attribute is missing")
		}
		return key.Attribute, nil, nil
	}
	node := key.Derived.Definition
	for {
		collated, ok := node.(collationExpression)
		if !ok {
			break
		}
		node = collated.child
	}
	if column, ok := node.(columnExpression); ok {
		a := column.origin.Address
		if a.ClassID != parent.Address().ClassID || a.ObjectID != parent.Address().ObjectID || !parent.hasAttribute(a.SubID) {
			return 0, nil, fmt.Errorf("stuntgres: index catalog column has a foreign origin")
		}
		return a.SubID, nil, nil
	}
	if node == nil {
		return 0, nil, fmt.Errorf("stuntgres: index expression is missing")
	}
	return 0, node, nil
}
