package engine

import (
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

// Acquiring an ancestor grant freezes edges below that ancestor. Reconcile
// after every wait, including the first-child marker's exact publication copy.
// No stage, statement view, row evaluation or publication exists in this phase.
func (e *Engine) admitInheritanceAlter(scope *StatementScope, op *session.Operation, auth AuthorizationView, raw *parser.AlterTable, preparatory bool) error {
	state := scope.inheritance.alter
	if !preparatory && len(state.Targets) == 1 {
		return nil
	}
	original := scope.statementMutation
	locked := map[ids.ObjectAddress]bool{state.Root.Address(): true}
	processed := make(map[ids.ObjectAddress]bool)
	state.Definition = table.InheritanceDefinitionReader{Base: auth.Catalog}
	if !preparatory {
		state.prepared = make(map[ids.ObjectAddress]*inheritancePreparedNode)
		state.Preparing = true
		defer func() { state.Preparing = false }()
	}
	for {
		e.mu.Lock()
		current := e.privateCatalogLocked(scope.owner.Catalog)
		if current.Epoch() != auth.Catalog.Epoch() {
			e.mu.Unlock()
			return ids.ErrCatalogReplaced
		}
		targets := table.InheritanceAlterOrder(current, state.Root)
		e.mu.Unlock()
		state.Targets = targets
		progress := false
		for index, observed := range targets {
			if processed[observed.Address()] {
				continue
			}
			incoming := state.Actions
			if index != 0 {
				incoming = table.InheritanceParentActions(&state.InheritanceAlterPlan, observed)
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
			actualCatalog := e.privateCatalogLocked(scope.owner.Catalog)
			actual, _ := actualCatalog.Relation(observed.Address())
			if actualCatalog.Epoch() != auth.Catalog.Epoch() || !table.InheritanceMarkerPublication(observed, actual) {
				e.mu.Unlock()
				return ids.ErrCatalogReplaced
			}
			currentAuth, err := e.statementAuthorizationLocked(op, raw)
			if err == nil && !preparatory && (index == 0 || table.InheritanceEntryOwner(incoming)) {
				err = currentAuth.Check(access.Requirement{Object: ref, OwnerOnly: true})
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
				if err := prepared.reader.Validate(actualCatalog); err != nil {
					return err
				}
			}
			state.Definition.Base = actualCatalog
			state.Descendant = index != 0
			state.Graph = table.AlterGraph(actual)
			planned := state.Actions
			if state.Descendant {
				planned, state.Graph, err = e.inheritedAlterActions(state, actual)
				if err != nil {
					return err
				}
			}
			if index != 0 && !table.InheritanceEntryOwner(incoming) && len(planned) != 0 {
				e.mu.Lock()
				err = currentAuth.Check(access.Requirement{Object: ref, OwnerOnly: true})
				e.mu.Unlock()
				if err != nil {
					return err
				}
			}
			scope.lockedRef, scope.lockedDescriptor = ref, actual
			state.Selected[actual.Address()] = true
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
		current = e.privateCatalogLocked(scope.owner.Catalog)
		for _, prepared := range state.prepared {
			if err := prepared.reader.Validate(current); err != nil {
				e.mu.Unlock()
				return err
			}
		}
		state.Targets = table.InheritanceAlterOrder(current, state.Root)
		e.mu.Unlock()
		scope.statementMutation = original
		return nil
	}
}
