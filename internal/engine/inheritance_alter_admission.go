package engine

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/parser"
)

func inheritanceEntryOwner(actions []parser.AlterTableAction) bool {
	for _, action := range actions {
		switch action.Action {
		case parser.AddColumn, parser.SetNotNull, parser.AddNotNull, parser.AddCheck, parser.AlterColumnType, parser.SetExpression, parser.DropExpression:
			return true
		}
	}
	return false
}

// Acquiring an ancestor grant freezes edges below that ancestor. Reconcile
// after every wait, including the first-child marker's exact publication copy.
// No stage, statement view, row evaluation or publication exists in this phase.
func (e *Engine) admitInheritanceAlter(scope *StatementScope, op *Operation, auth AuthorizationView, raw *parser.AlterTable, preparatory bool) error {
	state := scope.inheritanceAlter
	if !preparatory && len(state.targets) == 1 {
		return nil
	}
	original := scope.statementMutation
	locked := map[ids.ObjectAddress]bool{state.root.Address(): true}
	processed := make(map[ids.ObjectAddress]bool)
	state.definition = inheritanceDefinitionReader{base: auth.Catalog}
	if !preparatory {
		state.prepared = make(map[ids.ObjectAddress]*inheritancePreparedNode)
		state.preparing = true
		defer func() { state.preparing = false }()
	}
	for {
		e.mu.Lock()
		current := e.privateCatalogLocked(scope.owner.catalog)
		if current.Epoch() != auth.Catalog.Epoch() {
			e.mu.Unlock()
			return ids.ErrCatalogReplaced
		}
		targets := inheritanceAlterOrder(current, state.root)
		e.mu.Unlock()
		state.targets = targets
		progress := false
		for index, observed := range targets {
			if processed[observed.Address()] {
				continue
			}
			incoming := state.actions
			if index != 0 {
				incoming = inheritanceParentActions(state, observed)
				if !preparatory && len(incoming) == 0 {
					continue
				}
			}
			ref := original.lockedRef
			ref.Address = observed.Address()
			if !locked[observed.Address()] {
				if err := scope.acquireSequence(ref, lock.AccessExclusive, true); err != nil {
					return err
				}
				locked[observed.Address()] = true
			}
			e.mu.Lock()
			actualCatalog := e.privateCatalogLocked(scope.owner.catalog)
			actual, _ := actualCatalog.Relation(observed.Address())
			if actualCatalog.Epoch() != auth.Catalog.Epoch() || !inheritanceMarkerPublication(observed, actual) {
				e.mu.Unlock()
				return ids.ErrCatalogReplaced
			}
			currentAuth, err := e.statementAuthorizationLocked(op, raw)
			if err == nil && !preparatory && (index == 0 || inheritanceEntryOwner(incoming)) {
				err = currentAuth.Check(AccessRequirement{Object: ref, OwnerOnly: true})
			}
			e.mu.Unlock()
			if err != nil {
				return err
			}
			if preparatory {
				processed[actual.Address()] = true
				progress = true
				break
			}
			for _, prepared := range state.prepared {
				if err := prepared.reader.validate(actualCatalog); err != nil {
					return err
				}
			}
			state.definition.base = actualCatalog
			state.descendant = index != 0
			state.graph = cloneInheritance(actual.Inheritance)
			planned := state.actions
			if state.descendant {
				planned, state.graph, err = e.inheritedAlterActions(state, actual)
				if err != nil {
					return err
				}
			}
			if index != 0 && !inheritanceEntryOwner(incoming) && len(planned) != 0 {
				e.mu.Lock()
				err = currentAuth.Check(AccessRequirement{Object: ref, OwnerOnly: true})
				e.mu.Unlock()
				if err != nil {
					return err
				}
			}
			scope.lockedRef, scope.lockedDescriptor = ref, actual
			state.selected[actual.Address()] = true
			prepared, err := e.prepareInheritanceNode(scope, op, currentAuth, actual, planned)
			if err != nil {
				return err
			}
			state.prepared[actual.Address()] = prepared
			processed[actual.Address()] = true
			progress = true
			break // Capture actual newly admitted descendants before the next provider.
		}
		if progress {
			continue
		}
		// Every prepared ancestor is protected by its real acquired grant. A final
		// actual closure capture contains growth admitted during the last wait.
		e.mu.Lock()
		current = e.privateCatalogLocked(scope.owner.catalog)
		for _, prepared := range state.prepared {
			if err := prepared.reader.validate(current); err != nil {
				e.mu.Unlock()
				return err
			}
		}
		state.targets = inheritanceAlterOrder(current, state.root)
		e.mu.Unlock()
		scope.statementMutation = original
		return nil
	}
}

func inheritancePureDefinitionPreparation(actions []parser.AlterTableAction) bool {
	for _, action := range actions {
		switch action.Action {
		case parser.DropIdentity, parser.DropNotNull, parser.DropConstraint, parser.DropColumn, parser.AddColumn, parser.SetNotNull, parser.AddNotNull, parser.AddCheck, parser.AddIdentity, parser.SetIdentity, parser.AddUsingIndex:
		default:
			return false
		}
	}
	return true
}
