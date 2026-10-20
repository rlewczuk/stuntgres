package engine

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (c *EvalContext) EvalReturningWholeRow(e plan.ReturningWholeRowExpr) (sqltype.Value, error) {
	parent, err := returningRowContext(c, e.Scope, e.Frame)
	if err != nil {
		return sqltype.Value{}, err
	}
	if e.Merge && mergeReturningAbsent(parent, e.Side) || !e.Merge && e.Old && parent.OldRow == nil {
		return sqltype.Null(e.Usage.Type), nil
	}
	fields := make([]sqltype.Value, len(e.Members))
	for i, member := range e.Members {
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
			column := b.columnAtOf(relation, i, pos).(plan.ColumnExpr)
			column.Frame = frame
			members[i] = column
		} else {
			members[i] = b.outerReturningColumnAt(scope, i, pos, !old)
		}
	}
	info := b.info(sqltype.Usage{Type: descriptor.SQLType, Typmod: -1}, pos, expr.Properties{RowDependent: true})
	return plan.ReturningWholeRowExpr{Info: info, Members: members, Scope: 1, Frame: frame, Old: old, Merge: aliases.mode == returningMerge, Side: side}, true, nil
}
