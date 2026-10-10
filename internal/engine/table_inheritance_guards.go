package engine

import (
	"fmt"
	"slices"
	"stuntgres/internal/parser"
)

// Query sources recurse in PostgreSQL, unlike physical INSERT targets. Inspect
// stored view sources too: a view can predate the creation of a table's child.
func inheritanceScanGuard(catalog CatalogRead, relation *relationDescriptor) error {
	if relation.view == nil {
		if relation.relationKind == tableRelation && inheritanceHasChildren(catalog, relation.address) {
			return sqlError("0A000", "recursive table inheritance is unsupported")
		}
		return nil
	}
	return inheritanceViewScanGuard(catalog, relation, make(map[ObjectAddress]bool))
}

func inheritanceViewScanGuard(catalog CatalogRead, relation *relationDescriptor, visited map[ObjectAddress]bool) error {
	if relation.view == nil {
		return inheritanceScanGuard(catalog, relation)
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
		if err := inheritanceViewScanGuard(catalog, live, visited); err != nil {
			return err
		}
	}
	return nil
}

func inheritanceAlterGuard(catalog CatalogRead, relation *relationDescriptor) error {
	if inheritanceParticipant(catalog, relation) {
		return sqlError("0A000", "ALTER TABLE inheritance operations are unsupported")
	}
	return nil
}

// Dependency mutation sees the private descriptor authority, not stale base
// edges. Scan the overlay directly rather than allocating a second catalog.
func inheritanceStageAlterGuard(stage *CatalogStage, relation *relationDescriptor) error {
	if _, removed := stage.removals[relation.address]; removed {
		return nil
	}
	if replacement, ok := stage.descriptors[relation.address].(*relationDescriptor); ok {
		relation = replacement
	}
	if relation.inheritance != nil {
		return sqlError("0A000", "ALTER TABLE inheritance operations are unsupported")
	}
	for address, descriptor := range stage.base.state.descriptors {
		if _, removed := stage.removals[address]; removed {
			continue
		}
		if replacement, ok := stage.descriptors[address]; ok {
			descriptor = replacement
		}
		if child, ok := descriptor.(*relationDescriptor); ok && child.inheritance != nil && slices.Contains(child.inheritance.parents, relation.address) {
			return sqlError("0A000", "ALTER TABLE inheritance operations are unsupported")
		}
	}
	for address, descriptor := range stage.descriptors {
		if _, exists := stage.base.state.descriptors[address]; exists {
			continue
		}
		if _, removed := stage.removals[address]; removed {
			continue
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
		case parser.DropNotNull, parser.DropConstraint:
			if relation.inheritance == nil {
				continue
			}
			for _, constraint := range relation.notNullConstraints {
				matches := action.Action == parser.DropConstraint && constraint.Name == action.Constraint.Text
				if slot, exists := relation.index[action.Column.Text]; action.Action == parser.DropNotNull && exists {
					matches = constraint.Attribute == relation.attributes[slot]
				}
				if matches && len(relation.inheritance.notNull[constraint.Attribute].origins) != 0 {
					return sqlError("42P16", fmt.Sprintf(`cannot drop inherited constraint "%s" of relation "%s"`, constraint.Name, relation.name))
				}
			}
		}
	}
	return inheritanceAlterGuard(catalog, relation)
}
