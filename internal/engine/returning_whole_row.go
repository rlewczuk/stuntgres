package engine

import (
	"fmt"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// This provider is confined to INSERT RETURNING, with an actual named catalog
// descriptor. It does not introduce generic ROW or composite field syntax.
type returningWholeRowExpression struct {
	expressionInfo
	members      []BoundExpr
	scope, frame uint32
	old          bool
}

func (e returningWholeRowExpression) Eval(c *EvalContext) (sqltype.Value, error) {
	parent, err := returningRowContext(c, e.scope, e.frame)
	if err != nil {
		return sqltype.Value{}, err
	}
	if e.old && parent.OldRow == nil {
		return sqltype.Null(e.usage.Type), nil
	}
	fields := make([]sqltype.Value, len(e.members))
	for i, member := range e.members {
		fields[i], err = member.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	return sqltype.OwnedCompositeValue(e.usage.Type, fields)
}
func (b *expressionBinder) returningWholeRow(name string, relation *relationDescriptor, frame uint32, aliases *returningAliases, pos parser.Position) (BoundExpr, bool, error) {
	if aliases == nil || aliases.mode != returningInsert {
		return nil, false, nil
	}
	old := false
	switch {
	case name == aliases.target:
	case aliases.old != "" && name == aliases.old:
		old = true
	case aliases.new != "" && name == aliases.new:
	default:
		return nil, false, nil
	}
	view := b.bindingCatalog()
	descriptor, ok := view.state.descriptors[ObjectAddress{ClassID: TypeClassID, ObjectID: OID(relation.rowType)}].(*typeDescriptor)
	if !ok || descriptor.owningRelation != relation.address || descriptor.typ.CompositeLen() != len(relation.columns) {
		return nil, true, fmt.Errorf("stuntgres: RETURNING named row descriptor is missing")
	}
	b.returningReadRequired = true
	members := make([]BoundExpr, len(relation.columns))
	scope := &queryBindingScope{relation: relation, frame: frame, returning: aliases}
	for i := range members {
		members[i] = b.outerReturningColumnAt(scope, i, pos, !old)
	}
	info := b.info(sqltype.Usage{Type: descriptor.typ, Typmod: -1}, pos, ExprProperties{RowDependent: true})
	return returningWholeRowExpression{expressionInfo: info, members: members, scope: 1, frame: frame, old: old}, true, nil
}
