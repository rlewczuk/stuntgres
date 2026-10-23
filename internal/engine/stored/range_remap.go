package stored

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

func HasAliasRangeTypes(next *catalog.State) bool {
	for _, descriptor := range next.Descriptors() {
		if typ, ok := descriptor.(*catalog.Type); ok && typ.SQLType.RequiresRangeRemapValidation() {
			return true
		}
	}
	return false
}

// RangeIdentityConstraints captures actual endpoint relationships, not a global
// opt-in. Inspection is provider-free and cancellation-aware under Engine.mu.
func RangeIdentityConstraints(ctx context.Context, next *catalog.State, relations map[ids.ObjectAddress]*storage.Relation) ([]catalog.AliasOrderConstraint, error) {
	var constraints []catalog.AliasOrderConstraint
	var seen map[catalog.AliasOrderConstraint]bool
	segment := func(visit func(func(catalog.AliasOrderEndpoint) error) error) error {
		var previous catalog.AliasOrderEndpoint
		have := false
		return visit(func(point catalog.AliasOrderEndpoint) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if have && previous != point {
				edge := catalog.AliasOrderConstraint{Lower: previous, Upper: point}
				if !seen[edge] {
					if seen == nil {
						seen = make(map[catalog.AliasOrderConstraint]bool)
					}
					seen[edge] = true
					constraints = append(constraints, edge)
				}
			}
			previous, have = point, true
			return nil
		})
	}
	var valueOrder func(sqltype.Value) error
	valueOrder = func(value sqltype.Value) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if value.IsNull() || !value.Type().RequiresRangeRemapValidation() {
			return nil
		}
		typ := value.Type().BaseType()
		if typ.RangeType() != nil {
			// Published members already have normalized source order. Stream
			// their immutable bounds without copying or sorting their payload.
			return segment(func(leaf func(catalog.AliasOrderEndpoint) error) error {
				return value.VisitRangeEndpoints(func(value sqltype.Value) error {
					point, err := aliasRangeEndpoint(next, value)
					if err != nil {
						return err
					}
					return leaf(point)
				})
			})
		}
		if typ.IsArray() {
			for i := range value.ArrayLen() {
				if err := valueOrder(value.ArrayElement(i)); err != nil {
					return err
				}
			}
		}
		if typ.IsComposite() {
			for i := range value.CompositeLen() {
				if err := valueOrder(value.CompositeField(i)); err != nil {
					return err
				}
			}
		}
		return nil
	}
	for _, relation := range relations {
		for _, row := range relation.Versions {
			for _, value := range row.Values {
				if err := valueOrder(value); err != nil {
					return nil, err
				}
			}
		}
	}
	var failure error
	visit := func(root expr.Expr) {
		plan.WalkExpression(root, func(node expr.Expr) {
			if failure != nil {
				return
			}
			if literal, ok := node.(plan.LiteralExpr); ok {
				failure = valueOrder(literal.Value)
			}
			constructor, ok := node.(plan.RangeConstructorExpr)
			if !ok || !constructor.ResultUsage().Type.RequiresRangeRemapValidation() || failure != nil {
				return
			}
			// Decode source intervals, not adjacent leaves in written order.
			// Unknown arguments never leave partial companion relationships.
			datum, known, err := capturedAliasDatum(ctx, next, constructor)
			if err != nil {
				failure = err
				return
			}
			if known {
				failure = segment(func(leaf func(catalog.AliasOrderEndpoint) error) error {
					for _, point := range datum.points {
						if err := leaf(point); err != nil {
							return err
						}
					}
					return nil
				})
			}
		})
	}
	for _, descriptor := range next.Descriptors() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := VisitDefinitionExpressions(descriptor, visit); err != nil {
			return nil, err
		}
		if relation, ok := descriptor.(*catalog.Relation); ok {
			for _, generation := range relation.ColumnMeta.Generations {
				visit(generation.Planned)
			}
		}
		if failure != nil {
			return nil, failure
		}
	}
	return constraints, ctx.Err()
}

func aliasRangeEndpoint(next *catalog.State, value sqltype.Value) (catalog.AliasOrderEndpoint, error) {
	point := catalog.AliasOrderEndpoint{Domain: value.Type().BaseType().ID()}
	var id uint32
	var err error
	var class ids.OID
	switch point.Domain {
	case sqltype.Regclass:
		class = ids.RelationClassID
		id, err = value.ClassOID()
	case sqltype.Regtype:
		class = ids.TypeClassID
		id, err = value.TypeOID()
	case sqltype.Regnamespace, sqltype.Regcollation:
		address, failure := catalog.AliasAddress(value)
		class, id, err = address.ClassID, uint32(address.ObjectID), failure
	case sqltype.Regproc, sqltype.Regprocedure:
		class = ids.ProcedureClassID
		id, err = value.ProcedureOID()
	case sqltype.Regoper, sqltype.Regoperator:
		class = ids.OperatorClassID
		id, err = value.OperatorOID()
	case sqltype.Regdictionary:
		class = ids.TSDictionaryClassID
		id, err = value.RegTSOID()
	case sqltype.Regconfig:
		id, err = value.RegTSOID()
	case sqltype.Regrole:
		id, err = value.RoleOID()
	default:
		return catalog.AliasOrderEndpoint{}, fmt.Errorf("stuntgres: captured alias range endpoint lacks scalar identity")
	}
	point.OID = ids.OID(id)
	address := ids.ObjectAddress{ClassID: class, ObjectID: point.OID}
	if class != 0 && next.Get(address) != nil {
		point.Object = address
	}
	return point, err
}

// ValidateRemappedRanges certifies the complete detached candidate after all OID
// mapping, outside Engine.mu and before any replacement publication.
func ValidateRemappedRanges(ctx context.Context, next *catalog.State, relations map[ids.ObjectAddress]*storage.Relation) error {
	for _, relation := range relations {
		for _, row := range relation.Versions {
			for _, value := range row.Values {
				if err := value.ValidateMappedRanges(ctx); err != nil {
					return err
				}
			}
		}
	}
	var failure error
	visit := func(root expr.Expr) {
		if failure != nil {
			return
		}
		plan.WalkExpression(root, func(node expr.Expr) {
			if failure != nil {
				return
			}
			if literal, ok := node.(plan.LiteralExpr); ok {
				failure = literal.Value.ValidateMappedRanges(ctx)
			}
		})
	}
	for _, descriptor := range next.Descriptors() {
		if err := VisitDefinitionExpressions(descriptor, visit); err != nil {
			return err
		}
		if failure != nil {
			return failure
		}
		if relation, ok := descriptor.(*catalog.Relation); ok {
			for _, generation := range relation.ColumnMeta.Generations {
				visit(generation.Planned)
			}
		}
		if failure != nil {
			return failure
		}
	}
	return ctx.Err()
}

// Transparent generation/domain wrappers do not execute a conversion here.
// Arbitrary casts/callables never turn their incidental constants into bounds.
func capturedAliasLiteral(node expr.Expr) (sqltype.Value, bool) {
	switch node := node.(type) {
	case plan.LiteralExpr:
		return node.Value, true
	case plan.CoercionExpr:
		if node.Child.ResultUsage().Type.BaseType().ID() == node.ResultUsage().Type.BaseType().ID() {
			return capturedAliasLiteral(node.Child)
		}
	case plan.CollationExpr:
		return capturedAliasLiteral(node.Child)
	}
	return sqltype.Value{}, false
}
