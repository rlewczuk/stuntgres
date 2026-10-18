package engine

import (
	"fmt"
	"iter"
	"slices"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

// Ordinary LOCK and view-target paths retain their capability boundary.
// Direct UPDATE/DELETE targets use real command-local descendant admission.
func inheritanceTargetGuard(catalog CatalogRead, relation *relationDescriptor) error {
	if relation.View == nil {
		if relation.RelationKind == tableRelation && inheritanceHasChildren(catalog, relation.address) {
			return sqlError("0A000", "recursive table inheritance is unsupported")
		}
		return nil
	}
	return inheritanceViewTargetGuard(catalog, relation, make(map[ids.ObjectAddress]bool))
}

func inheritanceViewTargetGuard(catalog CatalogRead, relation *relationDescriptor, visited map[ids.ObjectAddress]bool) error {
	if relation.View == nil {
		return inheritanceTargetGuard(catalog, relation)
	}
	if visited[relation.address] {
		// The existing view binder owns recursive-view diagnostics.
		return nil
	}
	visited[relation.address] = true
	for _, source := range viewOf(relation).sources {
		live, ok := catalog.Relation(source.ref.Address)
		if !ok {
			return ids.ErrCatalogReplaced
		}
		if err := inheritanceViewTargetGuard(catalog, live, visited); err != nil {
			return err
		}
	}
	return nil
}

// Dependency mutation sees the private descriptor authority, not stale base
// edges. Scan the overlay directly rather than allocating a second catalog.
func inheritanceStageAlterGuard(stage *CatalogStage, relation *relationDescriptor) error {
	descriptors := func(yield func(ids.ObjectAddress, Descriptor) bool) {
		for address, descriptor := range stage.base.Descriptors() {
			if !yield(address, descriptor) {
				return
			}
		}
		for address, descriptor := range stage.descriptors {
			if _, exists := stage.base.Find(address); !exists {
				if !yield(address, descriptor) {
					return
				}
			}
		}
	}
	return inheritanceDependencyAlterGuard(descriptors, func(address ids.ObjectAddress) Descriptor { return stage.descriptors[address] }, func(address ids.ObjectAddress) bool { _, removed := stage.removals[address]; return removed }, relation)
}

func inheritanceDependencyAlterGuard(descriptors iter.Seq2[ids.ObjectAddress, Descriptor], replacement func(ids.ObjectAddress) Descriptor, removed func(ids.ObjectAddress) bool, relation *relationDescriptor) error {
	if removed(relation.address) {
		return nil
	}
	if current, ok := replacement(relation.address).(*relationDescriptor); ok {
		relation = current
	}
	if relation.Inheritance != nil {
		return sqlError("0A000", "ALTER TABLE inheritance operations are unsupported")
	}
	for address, descriptor := range descriptors {
		if removed(address) {
			continue
		}
		if current := replacement(address); current != nil {
			descriptor = current
		}
		if child, ok := descriptor.(*relationDescriptor); ok && child.Inheritance != nil && slices.Contains(child.Inheritance.parents, relation.address) {
			return sqlError("0A000", "ALTER TABLE inheritance operations are unsupported")
		}
	}
	return nil
}

func inheritanceColumnGuard(relation *relationDescriptor, name, operation string, position parser.Position) error {
	if relation.Inheritance == nil {
		return nil
	}
	slot, exists := relation.Index[name]
	if !exists || len(relation.Inheritance.columns[relation.Attributes[slot]].origins) == 0 {
		return nil
	}
	message := fmt.Sprintf(`cannot %s inherited column "%s"`, operation, name)
	if operation == "alter" {
		return expr.ErrorAt("42P16", message, position)
	}
	return sqlError("42P16", message)
}

// Check prohibited inherited actions before admitting any member of a list;
// no earlier action may evaluate defaults or stage a partial shape change.
func inheritanceAlterActionsGuard(catalog CatalogRead, relation *relationDescriptor, actions []parser.AlterTableAction) error {
	if rowSecurityActions(actions) {
		return nil
	}
	if !inheritanceParticipant(catalog, relation) {
		return nil
	}
	for _, action := range actions {
		switch action.Action {
		case parser.DropColumn, parser.AlterColumnType:
			operation := "drop"
			if action.Action == parser.AlterColumnType {
				operation = "alter"
			}
			if err := inheritanceColumnGuard(relation, action.Column.Text, operation, action.Column.Pos); err != nil {
				return err
			}
		case parser.DropExpression:
			if relation.Inheritance != nil {
				if slot, exists := relation.Index[action.Column.Text]; exists && len(relation.Inheritance.columns[relation.Attributes[slot]].origins) != 0 {
					return sqlError("42P16", "cannot drop generation expression from inherited column")
				}
			}
		case parser.DropNotNull, parser.DropConstraint:
			if action.Action == parser.DropConstraint && relation.Inheritance != nil {
				if provenance, exists := relation.Inheritance.checks[action.Constraint.Text]; exists && provenance.count() != 0 {
					return sqlError("42P16", fmt.Sprintf(`cannot drop inherited constraint "%s" of relation "%s"`, action.Constraint.Text, relation.name))
				}
			}
			if relation.Inheritance == nil {
				continue
			}
			for _, constraint := range relation.NotNullConstraints {
				matches := action.Action == parser.DropConstraint && constraint.Name == action.Constraint.Text
				if slot, exists := relation.Index[action.Column.Text]; action.Action == parser.DropNotNull && exists {
					matches = constraint.Attribute == relation.Attributes[slot]
				}
				if matches && relation.Inheritance.notNull[constraint.Attribute].count() != 0 {
					return sqlError("42P16", fmt.Sprintf(`cannot drop inherited constraint "%s" of relation "%s"`, constraint.Name, relation.name))
				}
			}
		}
	}
	return nil
}
