package engine

import (
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
)

// One statement-owned stage carries every candidate; the ordinary provider
// pipeline still owns cooking, row/index rewrites and physical validation.
// Nothing is installed in the transaction owner or live catalog between nodes.
type InheritanceAlterState struct {
	table.InheritanceAlterPlan
	Stage   *CatalogStage
	Changes *session.Journal
	// Prepared holds the root's prepared nodes, keyed by relation.
	Prepared map[ids.ObjectAddress]*table.InheritancePreparedNode
}

func (e *Engine) alterInheritanceLocked(scope *StatementScope, op *session.Operation, auth AuthorizationView, raw *parser.AlterTable, count int, tablespace ids.ObjectRef, types []table.AlterTypePreparation) (Result, error) {
	root := scope.LockedDescriptor.(*catalog.Relation)
	plan, preparatory, err := table.PlanInheritanceAlter(auth.Catalog, root, raw)
	if err != nil {
		e.Mu.Unlock()
		return Result{}, err
	}
	actions := plan.Actions
	original := scope.StatementMutation
	var stage *CatalogStage
	plan.Tablespace, plan.Types = tablespace, types
	state := &InheritanceAlterState{Changes: scope.Owner.Catalog.Clone(), InheritanceAlterPlan: plan}
	scope.Inheritance.Alter = state
	scope.Catalog = state.Changes
	e.Mu.Unlock()
	defer func() {
		scope.StatementMutation = original
		scope.Inheritance.Alter = nil
		e.DiscardCatalogStage(stage)
	}()
	// Definition preparation and every required physical grant precede the sole
	// stage. Preparatory grants remain distinct from actually visited owners.
	if err := e.admitInheritanceAlter(scope, op, auth, raw, preparatory); err != nil {
		return Result{}, err
	}
	if preparatory && table.InheritancePureDefinitionPreparation(actions) {
		if err := e.admitInheritanceAlter(scope, op, auth, raw, false); err != nil {
			return Result{}, err
		}
	}
	state.Events = make(map[ids.ObjectAddress][]table.InheritanceAlterEvent)
	state.Selected = make(map[ids.ObjectAddress]bool)
	e.Mu.Lock()
	current := e.PrivateCatalogLocked(scope.Owner.Catalog)
	stage = e.BeginCatalogStage(current)
	stage.AuthorizeLocked(auth, op.Session)
	state.Stage = stage
	e.Mu.Unlock()
	for index, old := range state.Targets {
		if index != 0 && len(table.InheritanceParentActions(&state.InheritanceAlterPlan, old)) == 0 {
			continue
		}
		e.Mu.Lock()
		current := e.PrivateCatalogLocked(scope.Owner.Catalog)
		if current.Get(old.Address()) != old {
			e.Mu.Unlock()
			return Result{}, ids.ErrCatalogReplaced
		}
		currentAuth, err := e.StatementAuthorizationLocked(op, raw)
		refForOwner := original.LockedRef
		refForOwner.Address = old.Address()
		incoming := actions
		if index != 0 {
			incoming = table.InheritanceParentActions(&state.InheritanceAlterPlan, old)
		}
		if err == nil && (index == 0 || table.InheritanceEntryOwner(incoming)) {
			err = currentAuth.Check(access.Requirement{Object: refForOwner, OwnerOnly: true})
		}
		e.Mu.Unlock()
		if err != nil {
			return Result{}, err
		}
		state.Selected[old.Address()] = true
		ref := original.LockedRef
		ref.Address = old.Address()
		state.Descendant = index != 0
		state.Graph = table.AlterGraph(old)
		planned := actions
		if cached := state.Prepared[old.Address()]; cached != nil {
			planned = cached.Actions
			state.Graph = catalog.CloneInheritance(cached.Graph)
		} else if state.Descendant {
			var err error
			planned, state.Graph, err = e.inheritedAlterActions(state, old)
			if err != nil {
				return Result{}, err
			}
		}
		if index != 0 && !table.InheritanceEntryOwner(incoming) && len(planned) != 0 {
			e.Mu.Lock()
			err = currentAuth.Check(access.Requirement{Object: refForOwner, OwnerOnly: true})
			e.Mu.Unlock()
			if err != nil {
				return Result{}, err
			}
		}
		scope.LockedRef, scope.LockedDescriptor = ref, old
		e.Mu.Lock()
		scope.LockedStorage = e.PrivateStorageLocked(scope.Owner.Catalog, old.Address())
		e.Mu.Unlock()
		if len(planned) == 0 {
			next, err := scope.finishInheritanceAlterCandidate(old, old)
			if err != nil {
				return Result{}, err
			}
			if next != old {
				if err := stage.Replace(next); err != nil {
					return Result{}, err
				}
			}
			continue
		}
		command := *raw
		command.Name = parser.ObjectName{Parts: []parser.Identifier{{Text: auth.Catalog.Get(old.Namespace()).Name()}, {Text: old.Name()}}}
		command.Actions = planned
		command.Only = true
		childAuth := auth
		childAuth.Catalog = e.StageRead(stage)
		childAuth.Recapture(childAuth.Catalog)
		e.Mu.Lock()
		if _, err := e.alterTableLocked(scope, op, childAuth, &command, count); err != nil {
			return Result{}, err
		}
	}
	scope.StatementMutation = original
	if err := scope.AcquireDefinitionProviders(stage); err != nil {
		return Result{}, err
	}
	if _, err := e.ValidateCatalogStage(op.Context(), stage); err != nil {
		return Result{}, err
	}
	e.Mu.Lock()
	if err := scope.ValidateLockedRelation(e); err != nil {
		e.Mu.Unlock()
		return Result{}, err
	}
	for _, old := range state.Targets {
		if state.Selected[old.Address()] && e.PrivateCatalogLocked(scope.Owner.Catalog).Get(old.Address()) != old {
			e.Mu.Unlock()
			return Result{}, ids.ErrCatalogReplaced
		}
	}
	for address, d := range stage.Descriptors() {
		e.CatalogChangeLocked(state.Changes, address, d)
	}
	for address := range stage.Removals() {
		e.CatalogChangeLocked(state.Changes, address, nil)
	}
	for address, rebound := range stage.RowShapes {
		state.Changes.Storage[address] = session.StorageChange{Original: rebound.Original, Root: rebound.Original.Versions, Ends: rebound.ObservedEnds, Generation: rebound.Generation, Replacement: rebound.Replacement, Relation: stage.Get(address).(*catalog.Relation)}
	}
	scope.Catalog = state.Changes
	e.Mu.Unlock()
	view.EmitCascade(op, stage.DropCascadeNames)
	return Result{}, nil
}

// inheritedAlterActions reads the members as staged, or as the definition
// preparation sees them before the stage begins.
func (e *Engine) inheritedAlterActions(state *InheritanceAlterState, old *catalog.Relation) ([]parser.AlterTableAction, *catalog.TableInheritance, error) {
	view := state.Definition.Base
	if state.Stage != nil {
		view = e.StageRead(state.Stage)
	}
	return table.InheritedAlterActions(&state.InheritanceAlterPlan, view, state.retiredBase(), old)
}

func (scope *StatementScope) finishInheritanceAlterCandidate(old, next *catalog.Relation) (*catalog.Relation, error) {
	state := scope.Inheritance.Alter
	if state == nil || old.Inheritance == nil && !state.Partition {
		return next, nil
	}
	return table.FinishInheritanceAlterCandidate(&state.InheritanceAlterPlan, scope.Engine().StageRead(state.Stage), old, next)
}
