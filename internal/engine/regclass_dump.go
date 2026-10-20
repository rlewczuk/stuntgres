package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
)

// regclassReplayReferences discovers only replay-order prerequisites. Opaque
// array/domain datums must be rendered after their live names exist, but must
// never acquire the DROP dependencies of a scalar regclass input constant.
// Numeric coercion inputs are integer/OID literals and therefore untouched.
func regclassReplayReferences(view catalog.Read, node expr.Expr, visit func(ids.ObjectAddress) error) error {
	var failure error
	plan.WalkExpression(node, func(child expr.Expr) {
		if failure != nil {
			return
		}
		literal, ok := child.(plan.LiteralExpr)
		if !ok {
			return
		}
		_, err := remapRegclassDatum(literal.Value, func(oid ids.OID) ids.OID {
			address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid}
			if _, exists := view.Descriptor(address); exists && failure == nil && oid != 0 {
				failure = visit(address)
			}
			return oid
		})
		if failure == nil {
			failure = err
		}
	})
	return failure
}
