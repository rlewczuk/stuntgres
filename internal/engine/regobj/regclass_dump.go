package regobj

import (
	"strconv"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

// ClassReplayReferences discovers only replay-order prerequisites. Opaque
// array/domain datums must be rendered after their live names exist, but must
// never acquire the DROP dependencies of a scalar regclass input constant.
// Numeric coercion inputs are integer/OID literals and therefore untouched.
func ClassReplayReferences(view catalog.Read, node expr.Expr, visit func(ids.ObjectAddress) error) error {
	var failure error
	plan.WalkExpression(node, func(child expr.Expr) {
		if failure != nil {
			return
		}
		literal, ok := child.(plan.LiteralExpr)
		if !ok {
			return
		}
		_, err := RemapClassDatum(literal.Value, func(oid ids.OID) ids.OID {
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

// Remap captured relation identities recursively without rendering or reparsing
// array text. Payloads are copied only on the first affected element.
func RemapClassDatum(value sqltype.Value, destination func(ids.OID) ids.OID) (sqltype.Value, error) {
	if value.IsNull() {
		return value, nil
	}
	if value.Type().BaseType().IsComposite() {
		return value.MapComposite(value.Type(), func(member sqltype.Value, _ *sqltype.Descriptor) (sqltype.Value, error) {
			return RemapClassDatum(member, destination)
		})
	}
	if value.Type().BaseType().IsArray() {
		return value.MapArray(value.Type(), func(element sqltype.Value, _ *sqltype.Descriptor) (sqltype.Value, error) {
			return RemapClassDatum(element, destination)
		})
	}
	if value.Type().BaseType().ID() != sqltype.Regclass {
		return value, nil
	}
	physical, err := value.BaseValue()
	if err != nil {
		return sqltype.Value{}, err
	}
	text, err := physical.Text()
	if err != nil {
		return sqltype.Value{}, err
	}
	oid, err := strconv.ParseUint(text, 10, 32)
	if err != nil {
		return sqltype.Value{}, err
	}
	target := destination(ids.OID(oid))
	if target == ids.OID(oid) {
		return value, nil
	}
	physical = sqltype.String(value.Type().BaseType(), strconv.FormatUint(uint64(target), 10))
	if value.Type().IsDomain() {
		return value.Type().Wrap(physical)
	}
	return physical, nil
}
