package table

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

func DroppedConstraint(parent *catalog.Relation, action parser.AlterTableAction) (ids.ObjectAddress, ids.ObjectAddress) {
	if action.Action == parser.DropConstraint {
		for _, c := range parent.Constraints.Unique {
			if c.Name == action.Constraint.Text {
				return ids.ObjectAddress{ClassID: 2606, ObjectID: c.OID}, c.Index
			}
		}
		for _, c := range parent.Constraints.Exclusion {
			if c.Name == action.Constraint.Text {
				return ids.ObjectAddress{ClassID: 2606, ObjectID: c.OID}, c.Index
			}
		}
		for _, c := range parent.Constraints.Check {
			if c.Name == action.Constraint.Text {
				return ids.ObjectAddress{ClassID: 2606, ObjectID: c.OID}, ids.ObjectAddress{}
			}
		}
		for _, c := range parent.Constraints.NotNull {
			if c.Name == action.Constraint.Text {
				return ids.ObjectAddress{ClassID: 2606, ObjectID: c.OID}, ids.ObjectAddress{}
			}
		}
	}
	return ids.ObjectAddress{}, ids.ObjectAddress{}
}

func HasDroppedConstraintIndexes(old *catalog.Relation, actions []parser.AlterTableAction) bool {
	for _, action := range actions {
		if constraint, _ := DroppedConstraint(old, action); constraint != (ids.ObjectAddress{}) {
			return true
		}
	}
	return false
}
