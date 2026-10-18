package engine

import "stuntgres/internal/engine/ids"

// regclassReplayReferences discovers only replay-order prerequisites. Opaque
// array/domain datums must be rendered after their live names exist, but must
// never acquire the DROP dependencies of a scalar regclass input constant.
// Numeric coercion inputs are integer/OID literals and therefore untouched.
func regclassReplayReferences(view CatalogRead, node BoundExpr, visit func(ids.ObjectAddress) error) error {
	var failure error
	walkBoundExpression(node, func(child BoundExpr) {
		if failure != nil {
			return
		}
		literal, ok := child.(literalExpression)
		if !ok {
			return
		}
		_, err := remapRegclassDatum(literal.value, func(oid ids.OID) ids.OID {
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
