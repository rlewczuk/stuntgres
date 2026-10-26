package tablecmds

import (
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

// Engine is core.Engine with the inheritance ALTER admission method: the
// read-only reference partition cancellation fixture waits on its
// "(*Engine).admitInheritanceAlter(" frame.
type Engine core.Engine

// Acquiring an ancestor grant freezes edges below that ancestor. Reconcile
// after every wait, including the first-child marker's exact publication copy.
// No stage, statement view, row evaluation or publication exists in this phase.
func (r *Engine) admitInheritanceAlter(scope *core.StatementScope, op *session.Operation, auth core.AuthorizationView, raw *parser.AlterTable, preparatory bool) error {
	e := (*core.Engine)(r)
	state := scope.Inheritance.Alter
	if !preparatory && len(state.Targets) == 1 {
		return nil
	}
	original := scope.StatementMutation
	locked := map[ids.ObjectAddress]bool{state.Root.Address(): true}
	processed := make(map[ids.ObjectAddress]bool)
	state.Definition = table.InheritanceDefinitionReader{Base: auth.Catalog}
	if !preparatory {
		state.Prepared = make(map[ids.ObjectAddress]*table.InheritancePreparedNode)
		state.Preparing = true
		defer func() { state.Preparing = false }()
	}
	for {
		e.Mu.Lock()
		current := e.PrivateCatalogLocked(scope.PendingCatalog())
		if current.Epoch() != auth.Catalog.Epoch() {
			e.Mu.Unlock()
			return ids.ErrCatalogReplaced
		}
		targets := table.InheritanceAlterOrder(current, state.Root)
		e.Mu.Unlock()
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
			ref := original.LockedRef
			ref.Address = observed.Address()
			if !locked[observed.Address()] {
				if err := scope.AcquireSequence(ref, lock.AccessExclusive, true); err != nil {
					return err
				}
				locked[observed.Address()] = true
			}
			e.Mu.Lock()
			actualCatalog := e.PrivateCatalogLocked(scope.PendingCatalog())
			actual, _ := actualCatalog.Relation(observed.Address())
			if actualCatalog.Epoch() != auth.Catalog.Epoch() || !table.InheritanceMarkerPublication(observed, actual) {
				e.Mu.Unlock()
				return ids.ErrCatalogReplaced
			}
			currentAuth, err := e.StatementAuthorizationLocked(op, raw)
			if err == nil && !preparatory && (index == 0 || table.InheritanceEntryOwner(incoming)) {
				err = currentAuth.Check(access.Requirement{Object: ref, OwnerOnly: true})
			}
			e.Mu.Unlock()
			if err != nil {
				return err
			}
			if preparatory {
				processed[actual.Address()] = true
				progress = true
				break
			}
			for _, prepared := range state.Prepared {
				if err := prepared.Reader.Validate(actualCatalog); err != nil {
					return err
				}
			}
			state.Definition.Base = actualCatalog
			state.Descendant = index != 0
			state.Graph = table.AlterGraph(actual)
			planned := state.Actions
			if state.Descendant {
				planned, state.Graph, err = inheritedAlterActions(e, state, actual)
				if err != nil {
					return err
				}
			}
			if index != 0 && !table.InheritanceEntryOwner(incoming) && len(planned) != 0 {
				e.Mu.Lock()
				err = currentAuth.Check(access.Requirement{Object: ref, OwnerOnly: true})
				e.Mu.Unlock()
				if err != nil {
					return err
				}
			}
			scope.LockedRef, scope.LockedDescriptor = ref, actual
			state.Selected[actual.Address()] = true
			prepared, err := table.PrepareInheritanceNode(inheritancePrepareHost{e: e, scope: scope, op: op, auth: currentAuth}, op, currentAuth.Catalog, &state.InheritanceAlterPlan, actual, planned)
			if err != nil {
				return err
			}
			state.Prepared[actual.Address()] = prepared
			processed[actual.Address()] = true
			progress = true
			break // Capture actual newly admitted descendants before the next provider.
		}
		if progress {
			continue
		}
		// Every prepared ancestor is protected by its real acquired grant. A final
		// actual closure capture contains growth admitted during the last wait.
		e.Mu.Lock()
		current = e.PrivateCatalogLocked(scope.PendingCatalog())
		for _, prepared := range state.Prepared {
			if err := prepared.Reader.Validate(current); err != nil {
				e.Mu.Unlock()
				return err
			}
		}
		state.Targets = table.InheritanceAlterOrder(current, state.Root)
		e.Mu.Unlock()
		scope.StatementMutation = original
		return nil
	}
}
