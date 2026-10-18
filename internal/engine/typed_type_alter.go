package engine

import (
	"fmt"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func (e *Engine) executeAlterComposite(scope *StatementScope, op *Operation, s *parser.AlterComposite, count int) (Result, error) {
	if count != 0 {
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	e.mu.Lock()
	auth, err := e.authorizationLocked(op)
	if err == nil {
		scope.routineAuth = auth
	}
	var typ *typeDescriptor
	var source *relationDescriptor
	if err == nil {
		var ref ids.ObjectRef
		ref, err = e.resolveRelationReference(auth, s.Name, true)
		if err == nil {
			source = auth.catalog.state.descriptors[ref.Address].(*relationDescriptor)
			if source.relationKind != compositeRelation {
				err = sqlError("42809", fmt.Sprintf(`"%s" is not a composite type`, source.name))
			} else {
				typ = auth.catalog.state.descriptors[ids.ObjectAddress{ClassID: TypeClassID, ObjectID: ids.OID(source.rowType)}].(*typeDescriptor)
			}
		}
	}
	if err == nil {
		err = auth.Check(AccessRequirement{Object: e.domain.Ref(auth.catalog.Epoch(), source.address), OwnerOnly: true})
	}
	e.mu.Unlock()
	if err != nil {
		return Result{}, err
	}
	if err := scope.acquireSequence(e.domain.Ref(auth.catalog.Epoch(), source.address), accessExclusive, true); err != nil {
		return Result{}, err
	}
	stage := e.BeginCatalogStage(auth.catalog)
	defer e.DiscardCatalogStage(stage)
	stage.authorizeLocked(auth, op.session)
	stage.domainBases = append(stage.domainBases, typ)
	stage.typedSource = typ.Address()
	stage.typedObserved = typedClosure(auth.catalog, typ.Address())
	targets := typedPropagationOrder(auth.catalog, typ.Address())
	if len(targets) != 0 {
		restrict := s.Rename != nil && s.Mode != parser.DropCascade
		for _, action := range s.Actions {
			restrict = restrict || action.Mode != parser.DropCascade
		}
		if restrict {
			diagnostic := sqlError("2BP01", fmt.Sprintf(`cannot alter type "%s" because it is the type of a typed table`, source.name)).(*sqlstate.Error)
			diagnostic.Hint = "Use ALTER ... CASCADE to alter the typed tables too."
			return Result{}, diagnostic
		}
	}
	// OID order grants deterministic closure authority; planning order below is
	// independently parent-before-child for multiple-parent inheritance.
	addresses := typedSortedAddresses(targets)
	for _, address := range addresses {
		if err := scope.acquireSequence(e.domain.Ref(auth.catalog.Epoch(), address), accessExclusive, true); err != nil {
			return Result{}, err
		}
	}
	e.mu.Lock()
	current := CatalogRead{owner: e, state: e.state}
	valid := e.state.epoch == auth.catalog.Epoch() && e.state.descriptors[source.address] == source && e.state.descriptors[typ.Address()] == typ && typedObservationValid(stage, current)
	e.mu.Unlock()
	if !valid {
		return Result{}, CatalogConflict{}
	}
	for _, action := range s.Actions {
		if action.Action == parser.AlterColumnType {
			if err := rowRewriteError(auth.catalog, source); err != nil {
				return Result{}, err
			}
		}
	}
	var sourceMutation typedRelationMutation
	if s.Rename != nil {
		next, renameErr := renameColumnCandidate(source, s.Column.Text, s.Rename.Text)
		if renameErr != nil {
			return Result{}, renameErr
		}
		if source.revision == ids.DefinitionRevision(^uint64(0)) {
			return Result{}, fmt.Errorf("stuntgres: definition revision exhausted")
		}
		next.revision = source.revision + 1
		sourceMutation = typedRelationMutation{old: source, next: next}
	} else {
		actions := make([]typedColumnAction, len(s.Actions))
		for i, action := range s.Actions {
			actions[i].action = action
		}
		sourceMutation, err = e.typedMutation(scope, op, auth, stage, source, actions)
		if err != nil {
			return Result{}, err
		}
	}
	for _, action := range s.Actions {
		name := action.Column.Text
		if action.Action == parser.AddColumn {
			name = action.Definition.Name
		}
		if action.Action == parser.DropColumn {
			continue
		}
		slot, exists := sourceMutation.next.index[name]
		if !exists {
			continue
		}
		usage := sourceMutation.next.columns[slot].Usage
		ref := e.domain.Ref(auth.catalog.Epoch(), ids.ObjectAddress{ClassID: TypeClassID, ObjectID: ids.OID(usage.Type.ID())})
		if err := auth.CanUse(ref); err != nil {
			return Result{}, err
		}
		stage.domainBases = append(stage.domainBases, auth.catalog.state.descriptors[ref.Address])
	}
	if err := stage.Replace(sourceMutation.next); err != nil {
		return Result{}, err
	}
	forced := make(map[ids.ObjectAddress]map[int32]bool, len(targets)+1)
	for _, action := range s.Actions {
		if action.Action == parser.AlterColumnType {
			if slot, ok := source.index[action.Column.Text]; ok {
				if forced[source.address] == nil {
					forced[source.address] = make(map[int32]bool)
				}
				forced[source.address][source.attributes[slot]] = true
			}
		}
	}
	for _, table := range targets {
		var actions []typedColumnAction
		var renames map[int32]string
		var graph *tableInheritance
		view := e.stageRead(stage)
		table = view.state.descriptors[table.address].(*relationDescriptor)
		if table.ofType == typ.Address() {
			actions, renames = typedShapeActions(source, sourceMutation.next, table, forced[source.address])
		} else {
			actions, renames, graph, err = typedInheritedActions(view, table, forced)
			if err != nil {
				return Result{}, err
			}
		}
		mutation, err := e.typedMutation(scope, op, auth, stage, table, actions)
		if err != nil {
			return Result{}, err
		}
		if len(mutation.transforms) != 0 {
			if forced == nil {
				forced = make(map[ids.ObjectAddress]map[int32]bool)
			}
			forced[table.address] = typedTransformAttributes(mutation.transforms)
		}
		for _, attribute := range table.attributes {
			destination, rename := renames[attribute]
			if !rename {
				continue
			}
			slot := mutation.next.indexForAttribute(attribute)
			if slot < 0 {
				continue
			}
			mutation.next, err = renameColumnCandidate(mutation.next, mutation.next.columns[slot].Name, destination)
			if err != nil {
				return Result{}, err
			}
		}
		if graph != nil || len(renames) != 0 {
			next := *mutation.next
			if table.revision == ids.DefinitionRevision(^uint64(0)) {
				return Result{}, fmt.Errorf("stuntgres: definition revision exhausted")
			}
			next.revision = table.revision + 1
			mutation.next = &next
			if graph != nil {
				finishTypedInheritance(view, &next, graph)
			}
		}
		if err := stage.Replace(mutation.next); err != nil {
			return Result{}, err
		}
		if err := e.stageTypedRows(scope, auth, stage, mutation); err != nil {
			return Result{}, err
		}
	}
	if err := scope.acquireDefinitionProviders(stage); err != nil {
		return Result{}, err
	}
	if scope.replacementRead {
		e.replacementMu.RUnlock()
		scope.replacementRead = false
	}
	prepared, err := e.ValidateCatalogStage(op.Context(), stage)
	if err == nil {
		_, err = e.publishCatalogOperation(op, scope, prepared, PublicationToken{Epoch: stage.base.Epoch()})
	}
	if err == nil {
		scope.completed = true
	}
	return Result{}, err
}
