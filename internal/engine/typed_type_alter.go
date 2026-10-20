package engine

import (
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func (e *Engine) executeAlterComposite(scope *StatementScope, op *Operation, s *parser.AlterComposite, count int) (Result, error) {
	if count != 0 {
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	e.mu.Lock()
	auth, err := e.authorizationLocked(op)
	var path []ids.ObjectAddress
	if err == nil {
		scope.routineAuth = auth
		path, err = e.referencePath(auth)
	}
	var typ *catalog.Type
	var source *catalog.Relation
	if err == nil {
		var ref ids.ObjectRef
		ref, err = e.resolveRelationReference(auth, s.Name, true)
		if err == nil {
			source = auth.Catalog.MustRelation(ref.Address)
			if source.RelationKind != catalog.CompositeRelation {
				err = sqlError("42809", fmt.Sprintf(`"%s" is not a composite type`, source.Name()))
			} else {
				typ = auth.Catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(source.RowType)})
			}
		}
	}
	if err == nil {
		err = auth.Check(access.Requirement{Object: e.catalog.Domain.Ref(auth.Catalog.Epoch(), source.Address()), OwnerOnly: true})
	}
	e.mu.Unlock()
	if err != nil {
		return Result{}, err
	}
	if err := scope.acquireSequence(e.catalog.Domain.Ref(auth.Catalog.Epoch(), source.Address()), lock.AccessExclusive, true); err != nil {
		return Result{}, err
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	defer e.DiscardCatalogStage(stage)
	stage.authorizeLocked(auth, op.session)
	stage.dependencyPath = path
	stage.domainBases = append(stage.domainBases, typ)
	stage.typedState().Source = typ.Address()
	stage.typedState().Observed = typedClosure(auth.Catalog, typ.Address())
	targets := typedPropagationOrder(auth.Catalog, typ.Address())
	if len(targets) != 0 {
		restrict := s.Rename != nil && s.Mode != parser.DropCascade
		for _, action := range s.Actions {
			restrict = restrict || action.Mode != parser.DropCascade
		}
		if restrict {
			diagnostic := sqlError("2BP01", fmt.Sprintf(`cannot alter type "%s" because it is the type of a typed table`, source.Name())).(*sqlstate.Error)
			diagnostic.Hint = "Use ALTER ... CASCADE to alter the typed tables too."
			return Result{}, diagnostic
		}
	}
	// OID order grants deterministic closure authority; planning order below is
	// independently parent-before-child for multiple-parent inheritance.
	addresses := typedef.TypedSortedAddresses(targets)
	for _, address := range addresses {
		if err := scope.acquireSequence(e.catalog.Domain.Ref(auth.Catalog.Epoch(), address), lock.AccessExclusive, true); err != nil {
			return Result{}, err
		}
	}
	e.mu.Lock()
	current := e.catalog.read(e.catalog.state)
	valid := e.catalog.state.Epoch() == auth.Catalog.Epoch() && e.catalog.state.Get(source.Address()) == source && e.catalog.state.Get(typ.Address()) == typ && typedObservationValid(stage, current)
	e.mu.Unlock()
	if !valid {
		return Result{}, catalog.Conflict{}
	}
	// Source ownership does not extend to typed tables or their descendants.
	// PostgreSQL checks them while recursing each subcommand, so ALTER
	// ATTRIBUTE TYPE first resolves its new type's USAGE.
	ownersChecked := false
	checkOwners := func() error {
		if ownersChecked {
			return nil
		}
		ownersChecked = true
		for _, address := range addresses {
			if err := auth.Check(access.Requirement{Object: e.catalog.Domain.Ref(auth.Catalog.Epoch(), address), OwnerOnly: true}); err != nil {
				return err
			}
		}
		return nil
	}
	if s.Rename != nil || len(s.Actions) == 0 || s.Actions[0].Action != parser.AlterColumnType {
		if err := checkOwners(); err != nil {
			return Result{}, err
		}
	}
	for _, action := range s.Actions {
		if action.Action == parser.AlterColumnType {
			if err := typedef.RowRewriteError(auth.Catalog, source); err != nil {
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
		if source.Revision() == ids.DefinitionRevision(^uint64(0)) {
			return Result{}, fmt.Errorf("stuntgres: definition revision exhausted")
		}
		next.ObjectRevision = source.Revision() + 1
		sourceMutation = typedRelationMutation{old: source, next: next}
	} else {
		actions := make([]typedef.TypedColumnAction, len(s.Actions))
		for i, action := range s.Actions {
			actions[i].Action = action
		}
		sourceMutation, err = e.typedMutation(scope, op, auth, stage, source, actions)
		if err != nil {
			return Result{}, err
		}
	}
	for i, action := range s.Actions {
		if i == 1 {
			if err := checkOwners(); err != nil {
				return Result{}, err
			}
		}
		name := action.Column.Text
		if action.Action == parser.AddColumn {
			name = action.Definition.Name
		}
		if action.Action == parser.DropColumn {
			continue
		}
		slot, exists := sourceMutation.next.Index[name]
		if !exists {
			continue
		}
		usage := sourceMutation.next.Columns[slot].Usage
		ref := e.catalog.Domain.Ref(auth.Catalog.Epoch(), ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(usage.Type.ID())})
		if err := auth.CanUse(ref); err != nil {
			return Result{}, err
		}
		stage.domainBases = append(stage.domainBases, auth.Catalog.Get(ref.Address))
	}
	if err := checkOwners(); err != nil {
		return Result{}, err
	}
	if err := stage.Replace(sourceMutation.next); err != nil {
		return Result{}, err
	}
	forced := make(map[ids.ObjectAddress]map[int32]bool, len(targets)+1)
	for _, action := range s.Actions {
		if action.Action == parser.AlterColumnType {
			if slot, ok := source.Index[action.Column.Text]; ok {
				if forced[source.Address()] == nil {
					forced[source.Address()] = make(map[int32]bool)
				}
				forced[source.Address()][source.Attributes[slot]] = true
			}
		}
	}
	for _, table := range targets {
		var actions []typedef.TypedColumnAction
		var renames map[int32]string
		var graph *catalog.TableInheritance
		view := e.stageRead(stage)
		table = view.MustRelation(table.Address())
		if table.OfType == typ.Address() {
			actions, renames = typedef.TypedShapeActions(source, sourceMutation.next, table, forced[source.Address()])
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
			forced[table.Address()] = typedTransformAttributes(mutation.transforms)
		}
		for _, attribute := range table.Attributes {
			destination, rename := renames[attribute]
			if !rename {
				continue
			}
			slot := mutation.next.IndexForAttribute(attribute)
			if slot < 0 {
				continue
			}
			mutation.next, err = renameColumnCandidate(mutation.next, mutation.next.Columns[slot].Name, destination)
			if err != nil {
				return Result{}, err
			}
		}
		if graph != nil || len(renames) != 0 {
			next := *mutation.next
			if table.Revision() == ids.DefinitionRevision(^uint64(0)) {
				return Result{}, fmt.Errorf("stuntgres: definition revision exhausted")
			}
			next.ObjectRevision = table.Revision() + 1
			mutation.next = &next
			if graph != nil {
				typedef.FinishTypedInheritance(view, &next, graph)
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
		_, err = e.publishCatalogOperation(op, scope, prepared, PublicationToken{Epoch: stage.Base.Epoch()})
	}
	if err == nil {
		emitViewCascade(op, stage.viewCascadeNames)
		scope.completed = true
	}
	return Result{}, err
}
