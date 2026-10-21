package engine

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
)

// PostgreSQL ComputeIndexAttrs strips top-level CollateExpr before deciding
// whether a key is an attribute or an indexprs entry. Do not strip coercions or
// use execution simplification: native execution/original trees stay untouched.
// Both catalog projection and compact index deparsing use this one decision.
func CatalogKey(parent *catalog.Relation, key catalog.IndexKey) (int32, expr.Expr, error) {
	if key.Derived == nil {
		if !parent.HasAttribute(key.Attribute) {
			return 0, nil, fmt.Errorf("stuntgres: index attribute is missing")
		}
		return key.Attribute, nil, nil
	}
	node := key.Derived.Definition
	for {
		collated, ok := node.(plan.CollationExpr)
		if !ok {
			break
		}
		node = collated.Child
	}
	if column, ok := node.(plan.ColumnExpr); ok {
		a := column.Origin.Address
		if a.ClassID != parent.Address().ClassID || a.ObjectID != parent.Address().ObjectID || !parent.HasAttribute(a.SubID) {
			return 0, nil, fmt.Errorf("stuntgres: index catalog column has a foreign origin")
		}
		return a.SubID, nil, nil
	}
	if node == nil {
		return 0, nil, fmt.Errorf("stuntgres: index expression is missing")
	}
	return 0, node, nil
}
