package engine

import (
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

// One statement-owned stage carries every candidate; the ordinary provider
// pipeline still owns cooking, row/index rewrites and physical validation.
// Nothing is installed in the transaction owner or live catalog between nodes.
type inheritanceAlterState struct {
	table.InheritanceAlterPlan
	stage   *CatalogStage
	changes *session.Journal
	// prepared holds the root's prepared nodes, keyed by relation.
	prepared map[ids.ObjectAddress]*inheritancePreparedNode
}

func (e *Engine) alterInheritanceLocked(scope *StatementScope, op *session.Operation, auth AuthorizationView, raw *parser.AlterTable, count int, tablespace ids.ObjectRef, types []table.AlterTypePreparation) (Result, error) {
	root := scope.lockedDescriptor.(*catalog.Relation)
	plan, preparatory, err := table.PlanInheritanceAlter(auth.Catalog, root, raw)
	if err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	actions := plan.Actions
	original := scope.statementMutation
	var stage *CatalogStage
	plan.Tablespace, plan.Types = tablespace, types
	state := &inheritanceAlterState{changes: scope.owner.Catalog.Clone(), InheritanceAlterPlan: plan}
	scope.inheritance.alter = state
	scope.catalog = state.changes
	e.mu.Unlock()
	defer func() {
		scope.statementMutation = original
		scope.inheritance.alter = nil
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
	e.mu.Lock()
	current := e.privateCatalogLocked(scope.owner.Catalog)
	stage = e.BeginCatalogStage(current)
	stage.authorizeLocked(auth, op.Session)
	state.stage = stage
	e.mu.Unlock()
	for index, old := range state.Targets {
		if index != 0 && len(table.InheritanceParentActions(&state.InheritanceAlterPlan, old)) == 0 {
			continue
		}
		e.mu.Lock()
		current := e.privateCatalogLocked(scope.owner.Catalog)
		if current.Get(old.Address()) != old {
			e.mu.Unlock()
			return Result{}, ids.ErrCatalogReplaced
		}
		currentAuth, err := e.statementAuthorizationLocked(op, raw)
		refForOwner := original.lockedRef
		refForOwner.Address = old.Address()
		incoming := actions
		if index != 0 {
			incoming = table.InheritanceParentActions(&state.InheritanceAlterPlan, old)
		}
		if err == nil && (index == 0 || table.InheritanceEntryOwner(incoming)) {
			err = currentAuth.Check(access.Requirement{Object: refForOwner, OwnerOnly: true})
		}
		e.mu.Unlock()
		if err != nil {
			return Result{}, err
		}
		state.Selected[old.Address()] = true
		ref := original.lockedRef
		ref.Address = old.Address()
		state.Descendant = index != 0
		state.Graph = catalog.CloneInheritance(old.Inheritance)
		planned := actions
		if cached := state.prepared[old.Address()]; cached != nil {
			planned = cached.actions
			state.Graph = catalog.CloneInheritance(cached.graph)
		} else if state.Descendant {
			var err error
			planned, state.Graph, err = e.inheritedAlterActions(state, old)
			if err != nil {
				return Result{}, err
			}
		}
		if index != 0 && !table.InheritanceEntryOwner(incoming) && len(planned) != 0 {
			e.mu.Lock()
			err = currentAuth.Check(access.Requirement{Object: refForOwner, OwnerOnly: true})
			e.mu.Unlock()
			if err != nil {
				return Result{}, err
			}
		}
		scope.lockedRef, scope.lockedDescriptor = ref, old
		e.mu.Lock()
		scope.lockedStorage = e.privateStorageLocked(scope.owner.Catalog, old.Address())
		e.mu.Unlock()
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
		childAuth.Catalog = e.stageRead(stage)
		childAuth.Recapture(childAuth.Catalog)
		e.mu.Lock()
		if _, err := e.alterTableLocked(scope, op, childAuth, &command, count); err != nil {
			return Result{}, err
		}
	}
	scope.statementMutation = original
	if err := scope.acquireDefinitionProviders(stage); err != nil {
		return Result{}, err
	}
	if _, err := e.ValidateCatalogStage(op.Context(), stage); err != nil {
		return Result{}, err
	}
	e.mu.Lock()
	if err := scope.validateLockedRelation(e); err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	for _, old := range state.Targets {
		if state.Selected[old.Address()] && e.privateCatalogLocked(scope.owner.Catalog).Get(old.Address()) != old {
			e.mu.Unlock()
			return Result{}, ids.ErrCatalogReplaced
		}
	}
	for address, d := range stage.Descriptors() {
		e.catalogChangeLocked(state.changes, address, d)
	}
	for address := range stage.Removals() {
		e.catalogChangeLocked(state.changes, address, nil)
	}
	for address, rebound := range stage.rowShapes {
		state.changes.Storage[address] = session.StorageChange{Original: rebound.original, Root: rebound.original.Versions, Ends: rebound.observedEnds, Generation: rebound.generation, Replacement: rebound.replacement, Relation: stage.Get(address).(*catalog.Relation)}
	}
	scope.catalog = state.changes
	e.mu.Unlock()
	emitViewCascade(op, stage.dropCascadeNames)
	return Result{}, nil
}

// inheritedAlterActions reads the members as staged, or as the definition
// preparation sees them before the stage begins.
func (e *Engine) inheritedAlterActions(state *inheritanceAlterState, old *catalog.Relation) ([]parser.AlterTableAction, *catalog.TableInheritance, error) {
	view := state.Definition.Base
	if state.stage != nil {
		view = e.stageRead(state.stage)
	}
	return table.InheritedAlterActions(&state.InheritanceAlterPlan, view, state.retiredBase(), old)
}

func (scope *StatementScope) finishInheritanceAlterCandidate(old, next *catalog.Relation) (*catalog.Relation, error) {
	state := scope.inheritance.alter
	if state == nil || old.Inheritance == nil {
		return next, nil
	}
	return table.FinishInheritanceAlterCandidate(&state.InheritanceAlterPlan, scope.engine().stageRead(state.stage), old, next)
}
