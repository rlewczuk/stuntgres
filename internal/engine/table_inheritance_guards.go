package engine

import (
	"fmt"
	"iter"
	"slices"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

// Ordinary LOCK and view-target paths retain their capability boundary.
// Direct UPDATE/DELETE targets use real command-local descendant admission.
func inheritanceTargetGuard(view catalog.Read, relation *catalog.Relation) error {
	if relation.View == nil {
		if relation.RelationKind == catalog.TableRelation && catalog.InheritanceHasChildren(view, relation.Address()) {
			return sqlError("0A000", "recursive table inheritance is unsupported")
		}
		return nil
	}
	return inheritanceViewTargetGuard(view, relation, make(map[ids.ObjectAddress]bool))
}

func inheritanceViewTargetGuard(catalog catalog.Read, relation *catalog.Relation, visited map[ids.ObjectAddress]bool) error {
	if relation.View == nil {
		return inheritanceTargetGuard(catalog, relation)
	}
	if visited[relation.Address()] {
		// The existing view binder owns recursive-view diagnostics.
		return nil
	}
	visited[relation.Address()] = true
	for _, source := range ViewOf(relation).Sources() {
		live, ok := catalog.Relation(source.Ref.Address)
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
func inheritanceStageAlterGuard(stage *CatalogStage, relation *catalog.Relation) error {
	descriptors := func(yield func(ids.ObjectAddress, catalog.Descriptor) bool) {
		for address, descriptor := range stage.Base.Descriptors() {
			if !yield(address, descriptor) {
				return
			}
		}
		for address, descriptor := range stage.Descriptors() {
			if _, exists := stage.Base.Find(address); !exists {
				if !yield(address, descriptor) {
					return
				}
			}
		}
	}
	return inheritanceDependencyAlterGuard(descriptors, func(address ids.ObjectAddress) catalog.Descriptor { return stage.Get(address) }, func(address ids.ObjectAddress) bool { _, removed := stage.Removal(address); return removed }, relation)
}

func inheritanceDependencyAlterGuard(descriptors iter.Seq2[ids.ObjectAddress, catalog.Descriptor], replacement func(ids.ObjectAddress) catalog.Descriptor, removed func(ids.ObjectAddress) bool, relation *catalog.Relation) error {
	if removed(relation.Address()) {
		return nil
	}
	if current, ok := replacement(relation.Address()).(*catalog.Relation); ok {
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
		if child, ok := descriptor.(*catalog.Relation); ok && child.Inheritance != nil && slices.Contains(child.Inheritance.Parents, relation.Address()) {
			return sqlError("0A000", "ALTER TABLE inheritance operations are unsupported")
		}
	}
	return nil
}

func inheritanceColumnGuard(relation *catalog.Relation, name, operation string, position parser.Position) error {
	if relation.Inheritance == nil {
		return nil
	}
	slot, exists := relation.Index[name]
	if !exists || len(relation.Inheritance.Columns[relation.Attributes[slot]].Origins) == 0 {
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
func inheritanceAlterActionsGuard(view catalog.Read, relation *catalog.Relation, actions []parser.AlterTableAction) error {
	if access.RowSecurityActions(actions) {
		return nil
	}
	if !catalog.InheritanceParticipant(view, relation) {
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
				if slot, exists := relation.Index[action.Column.Text]; exists && len(relation.Inheritance.Columns[relation.Attributes[slot]].Origins) != 0 {
					return sqlError("42P16", "cannot drop generation expression from inherited column")
				}
			}
		case parser.DropNotNull, parser.DropConstraint:
			if action.Action == parser.DropConstraint && relation.Inheritance != nil {
				if provenance, exists := relation.Inheritance.Checks[action.Constraint.Text]; exists && provenance.Count() != 0 {
					return sqlError("42P16", fmt.Sprintf(`cannot drop inherited constraint "%s" of relation "%s"`, action.Constraint.Text, relation.Name()))
				}
			}
			if relation.Inheritance == nil {
				continue
			}
			for _, constraint := range relation.Constraints.NotNull {
				matches := action.Action == parser.DropConstraint && constraint.Name == action.Constraint.Text
				if slot, exists := relation.Index[action.Column.Text]; action.Action == parser.DropNotNull && exists {
					matches = constraint.Attribute == relation.Attributes[slot]
				}
				if matches && relation.Inheritance.NotNull[constraint.Attribute].Count() != 0 {
					return sqlError("42P16", fmt.Sprintf(`cannot drop inherited constraint "%s" of relation "%s"`, constraint.Name, relation.Name()))
				}
			}
		}
	}
	return nil
}
