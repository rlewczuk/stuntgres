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

// DropConstraintIndexes retires the constraints that actions drop from old,
// with their backing indexes and dependents, and returns the parent as the
// drop leaves it. The same ordered provider retires named constraint identities, including
// constraints without backing indexes, during staging and pure preparation.
func DropConstraintIndexes(provider DropDependencyReader, old, next *catalog.Relation, actions []parser.AlterTableAction) (*catalog.Relation, error) {
	for _, action := range actions {
		constraint, address := DroppedConstraint(old, action)
		if constraint == (ids.ObjectAddress{}) {
			continue
		}
		if address != (ids.ObjectAddress{}) && provider.Removed(address) {
			continue // An earlier real column drop can retire the same backing.
		}
		if err := provider.Replace(next); err != nil {
			return next, err
		}
		retired := [2]ids.ObjectAddress{constraint, address}
		roots := retired[:1]
		if address != (ids.ObjectAddress{}) {
			if err := provider.Remove(provider.BaseRef(address)); err != nil {
				return next, err
			}
			roots = retired[:]
		}
		if err := provider.ExpressionDependencies(roots, action.Mode, constraint); err != nil {
			return next, err
		}
		// CASCADE can remove a self-reference from this same parent.
		next = provider.Descriptor(old.Address()).(*catalog.Relation)
	}
	return next, nil
}
