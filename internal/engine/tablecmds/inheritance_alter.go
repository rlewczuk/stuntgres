package tablecmds

import (
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
)

func alterInheritanceLocked(e *core.Engine, scope *core.StatementScope, op *session.Operation, auth core.AuthorizationView, raw *parser.AlterTable, count int, tablespace ids.ObjectRef, types []table.AlterTypePreparation) (result core.Result, err error) {
	root := scope.LockedDescriptor.(*catalog.Relation)
	plan, preparatory, err := table.PlanInheritanceAlter(auth.Catalog, root, raw)
	if err != nil {
		e.Mu.Unlock()
		return core.Result{}, err
	}
	actions := plan.Actions
	original := scope.StatementMutation
	var stage *core.CatalogStage
	var successors map[ids.ObjectAddress]bool
	plan.Tablespace, plan.Types = tablespace, types
	state := &core.InheritanceAlterState{InheritanceAlterPlan: plan}
	scope.Inheritance.Alter = state
	scope.Catalog = scope.PendingCatalog().Clone()
	e.Mu.Unlock()
	defer func() {
		scope.StatementMutation = original
		if err == nil && successors != nil {
			e.Mu.Lock()
			err = e.AdvanceAlterGrantsLocked(scope, successors)
			e.Mu.Unlock()
		}
		scope.Inheritance.Alter = nil
		e.DiscardCatalogStage(stage)
	}()
	// Definition preparation and every required physical grant precede the sole
	// stage. Preparatory grants remain distinct from actually visited owners.
	if err := (*Engine)(e).admitInheritanceAlter(scope, op, auth, raw, preparatory); err != nil {
		return core.Result{}, err
	}
	if preparatory && table.InheritancePureDefinitionPreparation(actions) {
		if err := (*Engine)(e).admitInheritanceAlter(scope, op, auth, raw, false); err != nil {
			return core.Result{}, err
		}
	}
	state.Events = make(map[ids.ObjectAddress][]table.InheritanceAlterEvent)
	state.Selected = make(map[ids.ObjectAddress]bool)
	e.Mu.Lock()
	current := e.PrivateCatalogLocked(scope.PendingCatalog())
	stage = e.BeginCatalogStage(current)
	stage.AuthorizeLocked(auth, op.Session)
	state.Stage = stage
	e.Mu.Unlock()
	var physicalWork []AlterTablePhysical
	if !table.RelationshipOnly(actions) {
		physicalWork = make([]AlterTablePhysical, 0, len(state.Targets))
	}
	for index, old := range state.Targets {
		if index != 0 && len(table.InheritanceParentActions(&state.InheritanceAlterPlan, old)) == 0 {
			continue
		}
		e.Mu.Lock()
		current := e.PrivateCatalogLocked(scope.PendingCatalog())
		if current.Get(old.Address()) != old {
			e.Mu.Unlock()
			return core.Result{}, ids.ErrCatalogReplaced
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
			return core.Result{}, err
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
			planned, state.Graph, err = inheritedAlterActions(e, state, old)
			if err != nil {
				return core.Result{}, err
			}
		}
		if index != 0 && !table.InheritanceEntryOwner(incoming) && len(planned) != 0 && !table.ValidationOnly(planned) {
			e.Mu.Lock()
			err = currentAuth.Check(access.Requirement{Object: refForOwner, OwnerOnly: true})
			e.Mu.Unlock()
			if err != nil {
				return core.Result{}, err
			}
		}
		scope.LockedRef, scope.LockedDescriptor = ref, old
		e.Mu.Lock()
		scope.LockedStorage = e.PrivateStorageLocked(scope.PendingCatalog(), old.Address())
		e.Mu.Unlock()
		if len(planned) == 0 {
			next, err := finishInheritanceAlterCandidate(scope, old, old)
			if err != nil {
				return core.Result{}, err
			}
			if next != old {
				if err := stage.Replace(next); err != nil {
					return core.Result{}, err
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
		if _, err := AlterTableLocked(e, scope, op, childAuth, &command, count, &physicalWork); err != nil {
			return core.Result{}, err
		}
	}
	scope.StatementMutation = original
	parents := make(map[ids.ObjectAddress]bool, len(physicalWork))
	validityOnly := make(map[ids.ObjectAddress]bool)
	for i := range physicalWork {
		parents[physicalWork[i].ref.Address] = true
		validityOnly[physicalWork[i].ref.Address] = physicalWork[i].validityOnly
	}
	layouts := stage.IndexesOrZero().Layouts
	phase, err := e.PublishAlterMetadata(scope, stage, parents, validityOnly)
	if err != nil {
		return core.Result{}, err
	}
	successors = phase.Targets
	for i := range physicalWork {
		if _, err := finishAlterPhysical(e, scope, phase, &physicalWork[i], layouts); err != nil {
			return core.Result{}, err
		}
	}
	for i := range physicalWork {
		if err := validateAlterPhysical(e, scope, phase, &physicalWork[i]); err != nil {
			return core.Result{}, err
		}
	}
	if err := e.FinishAlterValidation(scope, phase); err != nil {
		return core.Result{}, err
	}
	view.EmitCascade(op, stage.DropCascadeNames)
	return core.Result{}, nil
}

// inheritedAlterActions reads the members as staged, or as the definition
// preparation sees them before the stage begins.
func inheritedAlterActions(e *core.Engine, state *core.InheritanceAlterState, old *catalog.Relation) ([]parser.AlterTableAction, *catalog.TableInheritance, error) {
	view := state.Definition.Base
	if state.Stage != nil {
		view = e.StageRead(state.Stage)
	}
	return table.InheritedAlterActions(&state.InheritanceAlterPlan, view, retiredBase(state), old)
}

func finishInheritanceAlterCandidate(scope *core.StatementScope, old, next *catalog.Relation) (*catalog.Relation, error) {
	state := scope.Inheritance.Alter
	if state == nil || old.Inheritance == nil && !state.Partition {
		return next, nil
	}
	return table.FinishInheritanceAlterCandidate(&state.InheritanceAlterPlan, scope.Engine().StageRead(state.Stage), old, next)
}
