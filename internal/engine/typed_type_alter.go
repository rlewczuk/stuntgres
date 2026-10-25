package engine

import (
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func executeAlterComposite(e *Engine, scope *StatementScope, op *session.Operation, s *parser.AlterComposite, count int) (Result, error) {
	if count != 0 {
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	var path []ids.ObjectAddress
	if err == nil {
		scope.RoutineAuth = auth
		path, err = ReferencePath(auth)
	}
	var typ *catalog.Type
	var source *catalog.Relation
	if err == nil {
		var ref ids.ObjectRef
		ref, err = e.ResolveRelationReference(auth, s.Name, true)
		if err == nil {
			source = auth.Catalog.MustRelation(ref.Address)
			if source.RelationKind != catalog.CompositeRelation {
				err = SQLError("42809", fmt.Sprintf(`"%s" is not a composite type`, source.Name()))
			} else {
				typ = auth.Catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(source.RowType)})
			}
		}
	}
	if err == nil {
		err = auth.Check(access.Requirement{Object: e.Catalog.Domain.Ref(auth.Catalog.Epoch(), source.Address()), OwnerOnly: true})
	}
	e.Mu.Unlock()
	if err != nil {
		return Result{}, err
	}
	// Source metadata mutations share exact type authority with identity/drop.
	// Keep type -> real backing relation -> typed closure order on every route.
	if err := scope.AcquireSequence(e.Catalog.Domain.Ref(auth.Catalog.Epoch(), typ.Address()), lock.TypeExclusive, true); err != nil {
		return Result{}, err
	}
	if err := scope.AcquireSequence(e.Catalog.Domain.Ref(auth.Catalog.Epoch(), source.Address()), lock.AccessExclusive, true); err != nil {
		return Result{}, err
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	defer e.DiscardCatalogStage(stage)
	stage.AuthorizeLocked(auth, op.Session)
	stage.DependencyPath = path
	stage.DomainBases = append(stage.DomainBases, typ)
	typedStage(stage).Source = typ.Address()
	typedStage(stage).Observed = table.TypedClosure(auth.Catalog, typ.Address())
	targets := table.TypedPropagationOrder(auth.Catalog, typ.Address())
	if len(targets) != 0 {
		restrict := s.Rename != nil && s.Mode != parser.DropCascade
		for _, action := range s.Actions {
			restrict = restrict || action.Mode != parser.DropCascade
		}
		if restrict {
			diagnostic := SQLError("2BP01", fmt.Sprintf(`cannot alter type "%s" because it is the type of a typed table`, source.Name())).(*sqlstate.Error)
			diagnostic.Hint = "Use ALTER ... CASCADE to alter the typed tables too."
			return Result{}, diagnostic
		}
	}
	// OID order grants deterministic closure authority; planning order below is
	// independently parent-before-child for multiple-parent inheritance.
	addresses := typedef.TypedSortedAddresses(targets)
	for _, address := range addresses {
		if err := scope.AcquireSequence(e.Catalog.Domain.Ref(auth.Catalog.Epoch(), address), lock.AccessExclusive, true); err != nil {
			return Result{}, err
		}
	}
	e.Mu.Lock()
	currentAuth, admissionErr := e.AuthorizationLocked(op)
	if admissionErr == nil {
		current := currentAuth.Catalog
		if current.Epoch() != auth.Catalog.Epoch() || current.Get(source.Address()) != source || current.Get(typ.Address()) != typ || typ.OwningRelation != source.Address() || !table.TypedObservationValid(stage.Typed, current) {
			admissionErr = catalog.Conflict{}
		}
	}
	if admissionErr == nil {
		admissionErr = currentAuth.Check(access.Requirement{Object: e.Catalog.Domain.Ref(currentAuth.Catalog.Epoch(), source.Address()), OwnerOnly: true})
	}
	if admissionErr == nil {
		auth = currentAuth
		stage.AuthorizeLocked(auth, op.Session)
		scope.RoutineAuth = auth
	}
	e.Mu.Unlock()
	if admissionErr != nil {
		return Result{}, admissionErr
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
			if err := auth.Check(access.Requirement{Object: e.Catalog.Domain.Ref(auth.Catalog.Epoch(), address), OwnerOnly: true}); err != nil {
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
		next, renameErr := table.RenameColumnCandidate(source, s.Column.Text, s.Rename.Text)
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
		sourceMutation, err = typedMutation(e, scope, op, auth, stage, source, actions)
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
		ref := e.Catalog.Domain.Ref(auth.Catalog.Epoch(), ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(usage.Type.ID())})
		if err := auth.CanUse(ref); err != nil {
			return Result{}, err
		}
		stage.DomainBases = append(stage.DomainBases, auth.Catalog.Get(ref.Address))
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
	for _, tbl := range targets {
		var actions []typedef.TypedColumnAction
		var renames map[int32]string
		var graph *catalog.TableInheritance
		view := e.StageRead(stage)
		tbl = view.MustRelation(tbl.Address())
		if tbl.OfType == typ.Address() {
			actions, renames = typedef.TypedShapeActions(source, sourceMutation.next, tbl, forced[source.Address()])
		} else {
			actions, renames, graph, err = table.TypedInheritedActions(view, tbl, forced)
			if err != nil {
				return Result{}, err
			}
		}
		mutation, err := typedMutation(e, scope, op, auth, stage, tbl, actions)
		if err != nil {
			return Result{}, err
		}
		if len(mutation.transforms) != 0 {
			if forced == nil {
				forced = make(map[ids.ObjectAddress]map[int32]bool)
			}
			forced[tbl.Address()] = table.TypedTransformAttributes(mutation.transforms)
		}
		for _, attribute := range tbl.Attributes {
			destination, rename := renames[attribute]
			if !rename {
				continue
			}
			slot := mutation.next.IndexForAttribute(attribute)
			if slot < 0 {
				continue
			}
			mutation.next, err = table.RenameColumnCandidate(mutation.next, mutation.next.Columns[slot].Name, destination)
			if err != nil {
				return Result{}, err
			}
		}
		if graph != nil || len(renames) != 0 {
			next := *mutation.next
			if tbl.Revision() == ids.DefinitionRevision(^uint64(0)) {
				return Result{}, fmt.Errorf("stuntgres: definition revision exhausted")
			}
			next.ObjectRevision = tbl.Revision() + 1
			mutation.next = &next
			if graph != nil {
				typedef.FinishTypedInheritance(view, &next, graph)
			}
		}
		if err := stage.Replace(mutation.next); err != nil {
			return Result{}, err
		}
		if err := stageTypedRows(e, scope, auth, stage, mutation); err != nil {
			return Result{}, err
		}
	}
	if err := scope.AcquireDefinitionProviders(stage); err != nil {
		return Result{}, err
	}
	if scope.ReplacementRead {
		e.ReplacementMu.RUnlock()
		scope.ReplacementRead = false
	}
	prepared, err := e.ValidateCatalogStage(op.Context(), stage)
	if err == nil {
		_, err = e.PublishCatalogOperation(op, scope, prepared, PublicationToken{Epoch: stage.Base.Epoch()})
	}
	if err == nil {
		view.EmitCascade(op, stage.DropCascadeNames)
		scope.Completed = true
	}
	return Result{}, err
}
