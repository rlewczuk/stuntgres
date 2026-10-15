package engine

import (
	"fmt"
	"iter"
	"slices"
	"stuntgres/internal/parser"
)

// Ordinary LOCK and view-target paths retain their capability boundary.
// Direct UPDATE/DELETE targets use real command-local descendant admission.
func inheritanceTargetGuard(catalog CatalogRead, relation *relationDescriptor) error {
	if relation.view == nil {
		if relation.relationKind == tableRelation && inheritanceHasChildren(catalog, relation.address) {
			return sqlError("0A000", "recursive table inheritance is unsupported")
		}
		return nil
	}
	return inheritanceViewTargetGuard(catalog, relation, make(map[ObjectAddress]bool))
}

func inheritanceViewTargetGuard(catalog CatalogRead, relation *relationDescriptor, visited map[ObjectAddress]bool) error {
	if relation.view == nil {
		return inheritanceTargetGuard(catalog, relation)
	}
	if visited[relation.address] {
		// The existing view binder owns recursive-view diagnostics.
		return nil
	}
	visited[relation.address] = true
	for _, source := range relation.view.sources {
		live, ok := catalog.state.descriptors[source.ref.Address].(*relationDescriptor)
		if !ok {
			return ErrCatalogReplaced
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
	descriptors := func(yield func(ObjectAddress, Descriptor) bool) {
		for address, descriptor := range stage.base.state.descriptors {
			if !yield(address, descriptor) {
				return
			}
		}
		for address, descriptor := range stage.descriptors {
			if _, exists := stage.base.state.descriptors[address]; !exists {
				if !yield(address, descriptor) {
					return
				}
			}
		}
	}
	return inheritanceDependencyAlterGuard(descriptors, func(address ObjectAddress) Descriptor { return stage.descriptors[address] }, func(address ObjectAddress) bool { _, removed := stage.removals[address]; return removed }, relation)
}

func inheritanceDependencyAlterGuard(descriptors iter.Seq2[ObjectAddress, Descriptor], replacement func(ObjectAddress) Descriptor, removed func(ObjectAddress) bool, relation *relationDescriptor) error {
	if removed(relation.address) {
		return nil
	}
	if current, ok := replacement(relation.address).(*relationDescriptor); ok {
		relation = current
	}
	if relation.inheritance != nil {
		return sqlError("0A000", "ALTER TABLE inheritance operations are unsupported")
	}
	for address, descriptor := range descriptors {
		if removed(address) {
			continue
		}
		if current := replacement(address); current != nil {
			descriptor = current
		}
		if child, ok := descriptor.(*relationDescriptor); ok && child.inheritance != nil && slices.Contains(child.inheritance.parents, relation.address) {
			return sqlError("0A000", "ALTER TABLE inheritance operations are unsupported")
		}
	}
	return nil
}

func inheritanceColumnGuard(relation *relationDescriptor, name, operation string, position parser.Position) error {
	if relation.inheritance == nil {
		return nil
	}
	slot, exists := relation.index[name]
	if !exists || len(relation.inheritance.columns[relation.attributes[slot]].origins) == 0 {
		return nil
	}
	message := fmt.Sprintf(`cannot %s inherited column "%s"`, operation, name)
	if operation == "alter" {
		return expressionError("42P16", message, position)
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
			if relation.inheritance != nil {
				if slot, exists := relation.index[action.Column.Text]; exists && len(relation.inheritance.columns[relation.attributes[slot]].origins) != 0 {
					return sqlError("42P16", "cannot drop generation expression from inherited column")
				}
			}
		case parser.DropNotNull, parser.DropConstraint:
			if action.Action == parser.DropConstraint && relation.inheritance != nil {
				if provenance, exists := relation.inheritance.checks[action.Constraint.Text]; exists && provenance.count() != 0 {
					return sqlError("42P16", fmt.Sprintf(`cannot drop inherited constraint "%s" of relation "%s"`, action.Constraint.Text, relation.name))
				}
			}
			if relation.inheritance == nil {
				continue
			}
			for _, constraint := range relation.notNullConstraints {
				matches := action.Action == parser.DropConstraint && constraint.Name == action.Constraint.Text
				if slot, exists := relation.index[action.Column.Text]; action.Action == parser.DropNotNull && exists {
					matches = constraint.Attribute == relation.attributes[slot]
				}
				if matches && relation.inheritance.notNull[constraint.Attribute].count() != 0 {
					return sqlError("42P16", fmt.Sprintf(`cannot drop inherited constraint "%s" of relation "%s"`, constraint.Name, relation.name))
				}
			}
		}
	}
	return nil
}
