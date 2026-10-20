package engine

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (b *expressionBinder) returningWholeRow(name string, relation *catalog.Relation, frame uint32, aliases *returningAliases, pos parser.Position) (expr.Expr, bool, error) {
	if aliases == nil || aliases.mode != returningInsert && aliases.mode != returningMerge {
		return nil, false, nil
	}
	old := false
	side := exec.MergeDefaultSide
	switch {
	case name == aliases.target:
	case aliases.old != "" && name == aliases.old:
		old = true
		side = exec.MergeOldSide
	case aliases.new != "" && name == aliases.new:
		side = exec.MergeNewSide
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
		if aliases.mode == returningMerge && side == exec.MergeDefaultSide {
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
