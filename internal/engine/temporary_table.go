package engine

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
)

// Called under Engine.mu. Every return releases it, like the existing DDL path.
func (e *Engine) createTableLocked(scope *StatementScope, op *Operation, auth AuthorizationView, s *parser.CreateTable, count int) (Result, error) {
	if count != 0 {
		e.mu.Unlock()
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	changes := cloneCatalogChanges(scope.owner.catalog)
	originalCatalog := auth.Catalog
	persistence := s.Persistence
	if persistence == "" {
		persistence = "logged"
	}
	ns, err := e.relationCreationNamespaceLocked(auth, s.Name, persistence, changes)
	if err != nil {
		e.mu.Unlock()
		return Result{}, creationError(err, s.Name)
	}
	auth.Catalog = e.privateCatalogLocked(changes)
	auth.Recapture(auth.Catalog)
	if table, ok := changes.descriptors[ns.Address].(*catalog.Namespace); ok && !table.TemporaryToast && table.Temporary == op.session.token {
		auth.stagedTemporary = table.Address()
	}
	if op.replayOrZero().tableDestination {
		bootstrap, er := auth.Roles.Descriptor(auth.State.Authenticated)
		if er != nil || !bootstrap.Superuser || e.importTargets == nil {
			e.mu.Unlock()
			return Result{}, fmt.Errorf("stuntgres: invalid trusted restore destination authority")
		}
	}
	if err = auth.CheckCreate(ns); err != nil && !op.replayOrZero().tableDestination {
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
			return Result{}, expr.ErrorAt("0A000", "temporary partition definitions are unsupported", s.PartitionOf.Pos)
		}
		return Result{}, expr.ErrorAt("0A000", "temporary partition definitions are unsupported", pos.Pos)
	}
	if !temporary {
		// Permanent CREATE does not need the TEMP namespace overlay. Preserve
		// its admitted catalog identity for the existing importer/publication fences.
		auth.Catalog = originalCatalog
		auth.Recapture(auth.Catalog)
	}
	if !temporary && s.OnCommit != parser.OnCommitUnspecified {
		e.mu.Unlock()
		return Result{}, sqlError("42P16", "ON COMMIT can only be used on temporary tables")
	}
	if !temporary && scope.owner.state == TxActive {
		e.mu.Unlock()
		return Result{}, sqlError("0A000", "statement is unsupported in an explicit transaction")
	}
	key := ids.NameKey{ClassID: ids.RelationClassID, Namespace: ns.Address, Name: s.Name.Parts[len(s.Name.Parts)-1].Text}
	if _, exists := auth.Catalog.LookupName(key); exists {
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
	base := auth.Catalog
	stage := e.BeginCatalogStage(base)
	defer e.DiscardCatalogStage(stage)
	stage.authorizeLocked(auth, op.session)
	var partition *pendingPartitionDefinition
	if s.PartitionBy != nil || s.PartitionOf != nil {
		partition, err = e.schemaPartitionTableLocked(scope, auth, stage, s, ns.Address)
	} else {
		err = e.schemaTableLocked(op, auth, stage, s, ns.Address, auth.State.CurrentUser, scope.input, scope.foreignActionsOrZero().keyTargets)
	}
	e.mu.Unlock()
	if err != nil {
		return Result{}, err
	}
	if err = partition.finish(scope); err != nil {
		return Result{}, err
	}
	// Only the source helper's newly acquired reservation is incidental.
	// Preserve entry admission and acquire genuine definition-provider holds
	// afterward, under their existing publication/cancellation policy.
	replacementRead := scope.replacementRead
	if err = scope.acquireTypedSources(stage, false); err != nil {
		return Result{}, err
	}
	if !replacementRead && scope.replacementRead {
		e.replacementMu.RUnlock()
		scope.replacementRead = false
	}
	if err = scope.acquireDefinitionProviders(stage); err != nil {
		return Result{}, err
	}
	prepared, err := e.ValidateCatalogStage(op.Context(), stage)
	if err != nil {
		return Result{}, err
	}
	createdAddress, _ := stage.NameBinding(key)
	created := e.catalog.Domain.Ref(base.Epoch(), createdAddress)
	if temporary {
		scope.owner.accessedTemporary = true
		if len(stage.domainBases) != 0 {
			// The validated stage no longer mutates these observations. Retain
			// them through Finish without copying them into the outer transaction.
			scope.catalogAuthority = &catalogAuthority{
				session: stage.authSession, authorization: stage.authRevision,
				roles: stage.roleRevision, observations: stage.domainBases, databaseAuthority: stage.databaseAuthority, databasePredecessor: stage.databasePredecessor,
			}
		}
		if err := e.stageCatalogOverlay(scope, stage, changes); err != nil {
			return Result{}, err
		}
		return Result{created: created}, nil
	}
	_, err = e.publishCatalogOperation(op, scope, prepared, PublicationToken{Epoch: base.Epoch()})
	if err == nil {
		scope.completed = true
	}
	if _, conflict := err.(catalog.Conflict); conflict {
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
			_, exists := e.catalog.state.LookupName(key)
			same := resolveErr == nil && resolved.Address == ns.Address && e.catalog.state.Epoch() == base.Epoch()
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
	if err != nil {
		return Result{}, err
	}
	return Result{created: created}, nil
}

// Convert an already validated ordinary catalog stage into the transaction's
// immutable overlay. New stores use committed absence as their honest fence.
func (e *Engine) stageCatalogOverlay(scope *StatementScope, stage *CatalogStage, changes *catalogChanges) error {
	e.mu.Lock()
	if scope.txRef.Epoch() != e.catalog.state.Epoch() {
		e.mu.Unlock()
		return ids.ErrCatalogReplaced
	}
	if stage.initialTx != (ids.TxRef{}) && stage.initialTx != scope.txRef {
		e.mu.Unlock()
		return ids.ErrCatalogReplaced
	}
	err := e.captureCatalogStageLocked(stage, changes, e.privateCatalogLocked(scope.owner.catalog).State(), scope.catalogAuthority)
	if err == nil {
		e.stageCatalogChangesLocked(stage, changes)
	}
	e.mu.Unlock()
	if err != nil {
		return err
	}
	if stage.RemovalCount() > 0 {
		scope.discardRemovedWrites(stage)
	}
	if err = scope.projectCatalogOverlayShapes(stage, changes); err != nil {
		return err
	}
	if err = scope.reownDomainOverlayRows(stage, changes); err != nil {
		return err
	}
	if r := stage.materializedReplacement; r != nil {
		// A private CREATE WITH DATA has own rows on the old empty store.
		// REFRESH replaces that complete target, not an append to those rows.
		a := r.original.Address()
		if len(scope.ownWrites[a]) != 0 || scope.ownDeletes[a] != nil {
			scope.stageProjectedColumnRoots(a, nil, nil)
			delete(scope.stage.rows, a)
			delete(scope.stage.deletes, a)
		}
	}
	if len(stage.initialTables) > 0 {
		if scope.stage.rows == nil {
			scope.stage.rows = make(map[ids.ObjectAddress][]storage.RowVersion, len(stage.initialTables))
		}
		for a, store := range stage.initialTables {
			scope.stage.rows[a] = store.Versions
		}
	}
	if err = e.prepareCatalogOverlayIndexes(scope, stage, changes); err != nil {
		return err
	}
	e.mu.Lock()
	scope.op.session.mu.Lock()
	err = publicationOperationLocked(e, scope.op)
	if err == nil && scope.txRef.Epoch() != e.catalog.state.Epoch() {
		err = ids.ErrCatalogReplaced
	}
	if err == nil && stage.authSession != nil && (stage.authSession != scope.op.session || scope.op.session.authorization.revision != stage.authRevision || e.operationRolesLocked(scope.op).Revision != stage.roleRevision) {
		err = fmt.Errorf("stuntgres: authorization changed before publication")
	}
	if err == nil {
		err = e.catalogPreflightLocked(changes)
	}
	if err == nil {
		err = e.assignTransactionXIDLocked(scope.txRef)
	}
	if err == nil {
		scope.catalog = changes
		stage.lifecycle = catalogPublished
	}
	scope.op.session.mu.Unlock()
	e.mu.Unlock()
	return err
}

func (e *Engine) stageCatalogChangesLocked(stage *CatalogStage, changes *catalogChanges) {
	e.stageDefaultChangesLocked(stage, changes)
	for a, rowShape := range stage.rowShapes {
		shape, exists := changes.storage[a]
		if !exists {
			shape = catalogStorageChange{original: rowShape.original, root: rowShape.root, ends: rowShape.observedEnds, generation: rowShape.generation}
		}
		if shape.replacement != rowShape.replacement {
			shape.truncated = false
			shape.materializedConcurrent, shape.materializedContinuous = false, false
		}
		shape.replacement = rowShape.replacement
		shape.relation, _ = stage.Get(a).(*catalog.Relation)
		if shape.relation == nil {
			// An index-expression-only domain generation changes executable
			// index authority without changing its parent's column descriptor.
			shape.relation = stage.Base.MustRelation(a)
		}
		changes.storage[a] = shape
	}
	if r := stage.materializedReplacement; r != nil {
		a := r.original.Address()
		shape, exists := changes.storage[a]
		if !exists {
			shape = catalogStorageChange{original: r.predecessor, root: r.predecessor.Versions, ends: r.ends, generation: r.generation}
		}
		if !exists {
			shape.materializedContinuous = r.concurrent && r.predecessor == e.storage.Relations[a]
		} else {
			shape.materializedContinuous = shape.materializedContinuous && r.concurrent
		}
		shape.materializedConcurrent = r.concurrent
		// Keep the first committed predecessor (or genuine private CREATE
		// absence). A repeated refresh replaces only the latest private root.
		shape.replacement, shape.relation = r.storage, r.descriptor
		shape.truncated = false
		shape.rewritten, shape.frontier = true, e.visibility.CommitSeq()
		shape.snapshot, shape.hasSnapshot = nil, false
		changes.storage[a] = shape
	}
	for a, d := range stage.Descriptors() {
		e.catalogChangeLocked(changes, a, d)
		if table, ok := d.(*catalog.Relation); ok && table.RelationKind.Stored() && e.privateStorageLocked(changes, a) == nil {
			changes.storage[a] = catalogStorageChange{replacement: &storage.Relation{}, relation: table}
		}
	}
	for address, candidate := range stage.indexesOrZero().candidates {
		if changes.indexesOrZero().candidates == nil {
			changes.indexes().candidates = make(map[ids.ObjectAddress]indexRootCandidate)
		}
		if changes.indexesOrZero().uniqueRoots == nil {
			changes.indexes().uniqueRoots = make(map[ids.ObjectAddress]*storage.IndexRoot)
		}
		changes.indexes().candidates[address], changes.indexes().uniqueRoots[address] = candidate, candidate.root
	}
	for a := range stage.Removals() {
		e.catalogChangeLocked(changes, a, nil)
		delete(changes.generators, a)
		delete(changes.storage, a)
		changes.indexesSlot.forget(a)
		delete(changes.indexesOrZero().rebuilds, a)
	}
	for a, g := range stage.sequence.stagedGenerators() {
		changes.generators[a] = g
	}
}

func temporaryDataTarget(auth AuthorizationView, b *binding, stmt parser.Statement) bool {
	switch stmt.(type) {
	case *parser.Select:
		if !parser.HasModifyingCTE(stmt) {
			return false
		}
		for _, definition := range b.ctes {
			if mutation, ok := mutationBinding(definition); ok && !temporaryDataTarget(auth, mutation, definition.Statement) {
				return false
			}
		}
		return true
	case *parser.Copy, *parser.Insert, *parser.Update, *parser.Delete, *parser.Merge:
		if b.relation != nil {
			ns, ok := auth.Catalog.Namespace(b.relation.Namespace())
			return ok && ownTemporaryNamespace(auth, ns)
		}
	}
	return false
}

func (s *StatementScope) discardRemovedWrites(stage *CatalogStage) {
	rows, deletes := s.ownWrites, s.ownDeletes
	if s.projectedRoots {
		rows, deletes = s.stage.rows, s.stage.deletes
	}
	s.projectedRoots = true
	s.stage.rows = make(map[ids.ObjectAddress][]storage.RowVersion, len(rows))
	for a, values := range rows {
		if _, removed := stage.Removal(a); !removed {
			s.stage.rows[a] = values
		}
	}
	s.stage.deletes = make(map[ids.ObjectAddress]*visibility.PrivateDeletes, len(deletes))
	for a, root := range deletes {
		if _, removed := stage.Removal(a); !removed {
			s.stage.deletes[a] = root
		}
	}
}
