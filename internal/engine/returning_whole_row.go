package engine

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Named transition rows retain their lexical target owner and actual presence.
// This does not introduce generic ROW or composite field syntax.
type returningWholeRowExpression struct {
	expr.Info
	members      []expr.Expr
	scope, frame uint32
	old          bool
	merge        bool
	side         mergeReturningSide
}

func (e returningWholeRowExpression) Eval(ctx expr.Context) (sqltype.Value, error) {
	c := evalContext(ctx)
	parent, err := returningRowContext(c, e.scope, e.frame)
	if err != nil {
		return sqltype.Value{}, err
	}
	if e.merge && mergeReturningAbsent(parent, e.side) || !e.merge && e.old && parent.OldRow == nil {
		return sqltype.Null(e.Usage.Type), nil
	}
	fields := make([]sqltype.Value, len(e.members))
	for i, member := range e.members {
		fields[i], err = member.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	return sqltype.OwnedCompositeValue(e.Usage.Type, fields)
}
func (b *expressionBinder) returningWholeRow(name string, relation *catalog.Relation, frame uint32, aliases *returningAliases, pos parser.Position) (expr.Expr, bool, error) {
	if aliases == nil || aliases.mode != returningInsert && aliases.mode != returningMerge {
		return nil, false, nil
	}
	old := false
	side := mergeDefaultSide
	switch {
	case name == aliases.target:
	case aliases.old != "" && name == aliases.old:
		old = true
		side = mergeOldSide
	case aliases.new != "" && name == aliases.new:
		side = mergeNewSide
	default:
		return nil, false, nil
	}
	view := b.bindingCatalog()
	descriptor, ok := view.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(relation.RowType)})
	if !ok || descriptor.OwningRelation != relation.Address() || descriptor.SQLType.CompositeLen() != len(relation.Columns) {
		return nil, true, fmt.Errorf("stuntgres: RETURNING named row descriptor is missing")
	}
	b.target.returningReadRequired = true
	members := make([]expr.Expr, len(relation.Columns))
	scope := &queryBindingScope{relation: relation, frame: frame, returning: aliases}
	for i := range members {
		if aliases.mode == returningMerge && side == mergeDefaultSide {
			column := b.columnAtOf(relation, i, pos).(columnExpression)
			column.frame = frame
			members[i] = column
		} else {
			members[i] = b.outerReturningColumnAt(scope, i, pos, !old)
		}
	}
	info := b.info(sqltype.Usage{Type: descriptor.SQLType, Typmod: -1}, pos, expr.Properties{RowDependent: true})
	return returningWholeRowExpression{Info: info, members: members, scope: 1, frame: frame, old: old, merge: aliases.mode == returningMerge, side: side}, true, nil
}
