package engine

import (
	"context"
	"fmt"
	"slices"
	"stuntgres/internal/parser"
)

// Called under Engine.mu. Every return releases it, like the existing DDL path.
func (e *Engine) createTableLocked(scope *StatementScope, op *Operation, auth AuthorizationView, s *parser.CreateTable, count int) (Result, error) {
	if count != 0 {
		e.mu.Unlock()
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	changes := cloneCatalogChanges(scope.owner.catalog)
	originalCatalog := auth.catalog
	persistence := s.Persistence
	if persistence == "" {
		persistence = "logged"
	}
	ns, err := e.relationCreationNamespaceLocked(auth, s.Name, persistence, changes)
	if err != nil {
		e.mu.Unlock()
		return Result{}, creationError(err, s.Name)
	}
	auth.catalog = e.privateCatalogLocked(changes)
	if op.restoreTableDestination {
		bootstrap, er := auth.roles.Descriptor(auth.state.Authenticated)
		if er != nil || !bootstrap.Superuser || e.importTargets == nil {
			e.mu.Unlock()
			return Result{}, fmt.Errorf("stuntgres: invalid trusted restore destination authority")
		}
	}
	if err = auth.CheckCreate(ns); err != nil && !op.restoreTableDestination {
		e.mu.Unlock()
		return Result{}, creationError(err, s.Name)
	}
	persistence, err = adjustRelationCreationPersistence(auth, ns, persistence)
	if err != nil {
		e.mu.Unlock()
		return Result{}, creationError(err, s.Name)
	}
	temporary := persistence == "temporary"
	if temporary && (s.PartitionBy != nil || s.PartitionOf != nil) {
		e.mu.Unlock()
		pos := s.PartitionBy
		if s.PartitionOf != nil {
			return Result{}, expressionError("0A000", "temporary partition definitions are unsupported", s.PartitionOf.Pos)
		}
		return Result{}, expressionError("0A000", "temporary partition definitions are unsupported", pos.Pos)
	}
	if !temporary {
		// Permanent CREATE does not need the TEMP namespace overlay. Preserve
		// its admitted catalog identity for the existing importer/publication fences.
		auth.catalog = originalCatalog
	}
	if !temporary && s.OnCommit != parser.OnCommitUnspecified {
		e.mu.Unlock()
		return Result{}, sqlError("42P16", "ON COMMIT can only be used on temporary tables")
	}
	if !temporary && scope.owner.state == TxActive {
		e.mu.Unlock()
		return Result{}, sqlError("0A000", "statement is unsupported in an explicit transaction")
	}
	key := NameKey{ClassID: RelationClassID, Namespace: ns.Address, Name: s.Name.Parts[len(s.Name.Parts)-1].Text}
	if _, exists := auth.catalog.state.names[key]; exists {
		e.mu.Unlock()
		if s.IfNotExists {
			op.existingRelationNotice(key.Name)
			return Result{}, nil
		}
		return Result{}, sqlError("42P07", fmt.Sprintf(`relation "%s" already exists`, key.Name))
	}
	if s.PartitionBy != nil && len(s.Parents) != 0 {
		e.mu.Unlock()
		return Result{}, sqlError("42P17", "cannot create partitioned table as inheritance child")
	}
	base := auth.catalog
	stage := e.BeginCatalogStage(base)
	defer e.DiscardCatalogStage(stage)
	stage.authSession, stage.authRevision, stage.roleRevision = op.session, auth.revision, auth.roles.state.revision
	var partition *pendingPartitionDefinition
	if s.PartitionBy != nil || s.PartitionOf != nil {
		partition, err = e.schemaPartitionTableLocked(scope, auth, stage, s, ns.Address)
	} else {
		err = e.schemaTableLocked(op, auth, stage, s, ns.Address, auth.state.CurrentUser, scope.input)
	}
	e.mu.Unlock()
	if err != nil {
		return Result{}, err
	}
	if err = partition.finish(scope); err != nil {
		return Result{}, err
	}
	if err = scope.acquireDefinitionProviders(stage); err != nil {
		return Result{}, err
	}
	prepared, err := e.ValidateCatalogStage(op.Context(), stage)
	if err != nil {
		return Result{}, err
	}
	if temporary {
		scope.owner.accessedTemporary = true
		if err := e.stageTemporaryCatalog(scope, stage, changes); err != nil {
			return Result{}, err
		}
		return Result{}, nil
	}
	_, err = e.publishCatalogOperation(op, prepared, PublicationToken{Epoch: base.Epoch()})
	if err == nil {
		scope.completed = true
	}
	if _, conflict := err.(CatalogConflict); conflict {
		if err := op.Context().Err(); err != nil {
			return Result{}, err
		}
		if s.IfNotExists {
			e.mu.Lock()
			currentAuth, authErr := e.authorizationLocked(op)
			if authErr != nil {
				e.mu.Unlock()
				return Result{}, authErr
			}
			resolved, resolveErr := e.creationNamespace(currentAuth, s.Name)
			_, exists := e.state.names[key]
			same := resolveErr == nil && resolved.Address == ns.Address && e.state.epoch == base.Epoch()
			var accessErr error
			if same && exists {
				accessErr = currentAuth.CheckCreate(resolved)
			}
			contextErr := op.Context().Err()
			e.mu.Unlock()
			if contextErr != nil {
				return Result{}, contextErr
			}
			if resolveErr != nil {
				return Result{}, creationError(resolveErr, s.Name)
			}
			if accessErr != nil {
				return Result{}, creationError(accessErr, s.Name)
			}
			if same && exists {
				op.existingRelationNotice(key.Name)
				return Result{}, nil
			}
		}
		return Result{}, sqlError("42P07", fmt.Sprintf(`relation "%s" already exists`, key.Name))
	}
	return Result{}, err
}

// Convert an already validated ordinary catalog stage into the transaction's
// immutable overlay. New stores use committed absence as their honest fence.
func (e *Engine) stageTemporaryCatalog(scope *StatementScope, stage *CatalogStage, changes *catalogChanges) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if scope.txRef.epoch != e.state.epoch {
		return ErrCatalogReplaced
	}
	if err := e.stageTemporaryCatalogChangesLocked(scope.op.Context(), stage, changes); err != nil {
		return err
	}
	scope.catalog = changes
	return nil
}

func (e *Engine) stageTemporaryCatalogChangesLocked(ctx context.Context, stage *CatalogStage, changes *catalogChanges) error {
	for a, rowShape := range stage.rowShapes {
		shape, exists := changes.storage[a]
		if !exists {
			shape = catalogStorageChange{original: rowShape.original, root: rowShape.root, ends: rowShape.observedEnds, generation: rowShape.generation}
		}
		shape.replacement = rowShape.replacement
		shape.relation = stage.descriptors[a].(*relationDescriptor)
		changes.storage[a] = shape
	}
	for a, d := range stage.descriptors {
		e.catalogChangeLocked(changes, a, d)
		if table, ok := d.(*relationDescriptor); ok && table.relationKind == tableRelation && e.privateStorageLocked(changes, a) == nil {
			changes.storage[a] = catalogStorageChange{replacement: &relationStorage{}, relation: table}
		}
	}
	for a := range stage.removals {
		e.catalogChangeLocked(changes, a, nil)
		delete(changes.generators, a)
		delete(changes.storage, a)
		delete(changes.uniqueRoots, a)
	}
	for a, g := range stage.generators {
		changes.generators[a] = g
	}
	view := e.privateCatalogLocked(changes)
	for a, d := range view.state.descriptors {
		index, ok := d.(*relationDescriptor)
		if !ok || index.sqlIndex == nil {
			continue
		}
		parent := view.state.descriptors[index.sqlIndex.parent].(*relationDescriptor)
		oldParent, existed := stage.base.state.descriptors[parent.address].(*relationDescriptor)
		_, staged := stage.descriptors[a]
		if !staged && (!existed || slices.Equal(oldParent.attributes, parent.attributes)) {
			continue
		}
		if !index.sqlIndex.ready {
			delete(changes.uniqueRoots, a)
			continue
		}
		root, err := buildIndexRoot(ctx, parent, index.sqlIndex, e.privateStorageLocked(changes, parent.address))
		if err != nil {
			return err
		}
		if changes.uniqueRoots == nil {
			changes.uniqueRoots = make(map[ObjectAddress]*logicalIndexRoot)
		}
		changes.uniqueRoots[a] = root
	}
	return nil
}

func temporaryDataTarget(auth AuthorizationView, b *binding, stmt parser.Statement) bool {
	switch stmt.(type) {
	case *parser.Insert, *parser.Update, *parser.Delete:
		if b.relation != nil {
			ns, ok := auth.catalog.state.descriptors[b.relation.namespace].(*namespaceDescriptor)
			return ok && auth.session != nil && ns.temporary == auth.session.token
		}
	}
	return false
}

func (s *StatementScope) discardRemovedTemporaryWrites(stage *CatalogStage) {
	rows, deletes := s.ownWrites, s.ownDeletes
	if s.projectedRoots {
		rows, deletes = s.stage.rows, s.stage.deletes
	}
	s.projectedRoots = true
	s.stage.rows = make(map[ObjectAddress][]rowVersion, len(rows))
	for a, values := range rows {
		if _, removed := stage.removals[a]; !removed {
			s.stage.rows[a] = values
		}
	}
	s.stage.deletes = make(map[ObjectAddress]*privateDeletionRoot, len(deletes))
	for a, root := range deletes {
		if _, removed := stage.removals[a]; !removed {
			s.stage.deletes[a] = root
		}
	}
}
