package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func materializedRefreshAccess(auth AuthorizationView, ref ids.ObjectRef) error {
	d, ok := auth.catalog.Relation(ref.Address)
	if !ok || ref.Domain() != auth.engine.catalog.Domain || ref.Epoch != auth.catalog.Epoch() {
		return ids.ErrCatalogReplaced
	}
	if d.RelationKind != catalog.TableRelation && d.RelationKind != catalog.PartitionedRelation && d.RelationKind != catalog.MaterializedViewRelation {
		return sqlError("42809", fmt.Sprintf(`"%s" is not a table or materialized view`, d.Name()))
	}
	if err := auth.Check(AccessRequirement{Object: ref, Privileges: authorization.MaintainPrivilege}); err != nil {
		return err
	}
	if d.RelationKind != catalog.MaterializedViewRelation {
		return sqlError("0A000", fmt.Sprintf(`"%s" is not a materialized view`, d.Name()))
	}
	return nil
}

func (s *StatementScope) acquireMaterializedRefresh(stmt *parser.RefreshMaterializedView) error {
	e := s.op.session.Engine()
	e.mu.Lock()
	auth, err := e.authorizationLocked(s.op)
	var ref ids.ObjectRef
	var observed *catalog.Relation
	if err == nil {
		ref, err = e.resolveRelationReference(auth, stmt.Name, true)
	}
	if err == nil {
		err = materializedRefreshAccess(auth, ref)
	}
	if err == nil {
		observed = auth.catalog.MustRelation(ref.Address)
	}
	e.mu.Unlock()
	if err != nil {
		return err
	}
	policy := lock.Policy{TimeoutSlot: &s.op.lockWait, Timeout: s.op.lockTimeout, StatementDeadline: s.op.statementDeadline, Source: s.op.sourceContext}
	mode := lock.AccessExclusive
	if stmt.Concurrent.Character != 0 {
		mode = lock.Exclusive
	}
	request, err := e.locks.StartAcquire(s.op.Context(), s.ticket, lock.Resource{ObjectRef: ref}, mode, policy)
	if request != nil {
		if s.replacementRead {
			e.replacementMu.RUnlock()
			s.replacementRead = false
		}
		err = request.Wait()
	}
	if err != nil {
		return err
	}
	if !s.replacementRead {
		e.replacementMu.RLock()
		s.replacementRead = true
	}
	e.mu.Lock()
	auth, err = e.authorizationLocked(s.op)
	var current *catalog.Relation
	if err == nil {
		current, _ = auth.catalog.Relation(ref.Address)
	}
	if err == nil && (s.txRef.Epoch() != e.catalog.state.Epoch() || !materializedRefreshCompatible(observed, current)) {
		err = ids.ErrCatalogReplaced
	}
	if err == nil {
		err = materializedRefreshAccess(auth, ref)
	}
	if err == nil && stmt.Concurrent.Character != 0 {
		err = materializedConcurrentAdmission(auth.catalog, current, stmt.NoData)
	}
	if err == nil {
		s.lockedRef, s.lockedDescriptor, s.lockedStorage = ref, current, e.authorizationStorageLocked(auth, ref.Address)
	}
	e.mu.Unlock()
	if err != nil {
		return err
	}
	if !stmt.NoData {
		s.materializedRefresh = true
		return s.acquireViewSources()
	}
	return nil
}

// A preceding refresh changes rows/population/revision, not defining authority.
// Capture its new store only at target grant; never retry name resolution.
func materializedRefreshCompatible(old, current *catalog.Relation) bool {
	return current != nil && current.RelationKind == catalog.MaterializedViewRelation &&
		relationDefiningAuthorityCompatible(old, current)
}

// Immutable defining authority excludes compatible row/population publications.
// Shared by refresh and selected materialized ALTER admission; other statement
// admission does not acquire this compatibility fence.
func relationDefiningAuthorityCompatible(old, current *catalog.Relation) bool {
	return current != nil && current.Address() == old.Address() && current.Namespace() == old.Namespace() &&
		current.Name() == old.Name() && current.Owner == old.Owner &&
		current.View == old.View && current.RowType == old.RowType &&
		len(current.Columns) == len(old.Columns) && len(current.Attributes) == len(old.Attributes) &&
		(len(old.Columns) == 0 || &current.Columns[0] == &old.Columns[0]) &&
		(len(old.Attributes) == 0 || &current.Attributes[0] == &old.Attributes[0])
}

// Engine.mu is held on entry, and is consumed on every return.
func (e *Engine) refreshMaterializedLocked(scope *StatementScope, caller AuthorizationView, stmt *parser.RefreshMaterializedView, count int) (Result, error) {
	if count != 0 {
		e.mu.Unlock()
		return Result{}, fmt.Errorf("stuntgres: REFRESH MATERIALIZED VIEW takes no arguments")
	}
	ref := scope.lockedRef
	if err := materializedRefreshAccess(caller, ref); err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	d := caller.catalog.MustRelation(ref.Address)
	if d != scope.lockedDescriptor || e.authorizationStorageLocked(caller, ref.Address) != scope.lockedStorage {
		e.mu.Unlock()
		return Result{}, ids.ErrCatalogReplaced
	}
	concurrent := stmt.Concurrent.Character != 0
	var indices []*catalog.Relation
	var previous relationStorage
	var oldEntries []logicalIndexEntry
	var ownRows []rowVersion
	var ownDeletes *privateDeletionRoot
	frontier := e.commitSeq
	if concurrent {
		indices = materializedEligibleIndexes(caller.catalog, d)
		previous = *scope.lockedStorage
		var root *logicalIndexRoot
		if len(indices) != 0 {
			root = e.indexRoots[indices[0].Address()]
			if scope.owner.catalog != nil && scope.owner.catalog.uniqueRoots[indices[0].Address()] != nil {
				root = scope.owner.catalog.uniqueRoots[indices[0].Address()]
			}
		}
		if root == nil {
			e.mu.Unlock()
			return Result{}, fmt.Errorf("stuntgres: concurrent materialized index root is missing")
		}
		oldEntries = root.entries
		ownRows, ownDeletes = scope.columnProjectionRoots(d.Address())
	}
	stage := e.BeginCatalogStage(caller.catalog)
	stage.authorizeLocked(caller, scope.op.session)
	stage.querySources = scope.materializedSources
	stage.materializedSourceView = scope.materializedSourceView
	stage.indexOutput = scope.output
	defer e.DiscardCatalogStage(stage)
	var rows [][]sqltype.Value
	if stmt.NoData {
		e.mu.Unlock()
	} else {
		var err error
		rows, err = e.materializedRefreshRowsLocked(scope, ref, d)
		if err != nil {
			return Result{}, err
		}
	}
	if concurrent {
		var err error
		if len(ownRows) != 0 || ownDeletes != nil {
			versions := make([]rowVersion, 0, len(previous.versions)+len(ownRows))
			for _, row := range previous.versions {
				if liveVersion(row, frontier, previous.ends, ownDeletes) {
					versions = append(versions, row)
				}
			}
			for _, row := range ownRows {
				if liveVersion(row, frontier, nil, ownDeletes) {
					versions = append(versions, row)
				}
			}
			previous.versions, previous.ends = versions, nil
			evaluation := EvalContext{Context: scope.op.Context(), Scope: 1, Frame: 1, statementScope: scope, Input: scope.input, Output: scope.output, catalog: stage.Base}
			oldEntries, err = buildIndexEntries(&evaluation, d, indices[0].SQLIndex, &previous, nil)
			if err != nil {
				return Result{}, err
			}
		}
		rows, err = materializedConcurrentRows(scope.op.Context(), caller.catalog, d, &previous, frontier, oldEntries, indices, rows, scope.output)
		if err != nil {
			return Result{}, err
		}
	}
	if err := e.stageMaterializedRows(scope.op.Context(), stage, d, scope.lockedStorage, rows, !stmt.NoData, scope.txRef); err != nil {
		return Result{}, err
	}
	stage.materializedReplacement.concurrent = concurrent
	// Provider validation may replace the catalog. Exact stage fences, not this
	// admission lease, decide whether this original target can still publish.
	if scope.replacementRead {
		e.replacementMu.RUnlock()
		scope.replacementRead = false
	}
	prepared, err := e.ValidateCatalogStage(scope.op.Context(), stage)
	if err == nil {
		_, err = e.publishCatalogOperation(scope.op, scope, prepared, PublicationToken{Epoch: stage.Base.Epoch()})
	}
	if err == nil {
		scope.completed = true
	}
	return Result{}, err
}

// Population's effective principal and expression identity are restored before
// any stage validation/publication. No session settings or session_user change.
func (e *Engine) materializedRefreshRowsLocked(scope *StatementScope, ref ids.ObjectRef, d *catalog.Relation) ([][]sqltype.Value, error) {
	op := scope.op
	population, owner := op.materializedPopulation, op.materializedOwner
	identity, schema := scope.identity, scope.currentSchemaResult
	input := scope.input
	op.materializedPopulation, op.materializedOwner = true, d.Owner
	scope.currentSchemaResult = sqltype.Value{}
	defer func() {
		op.materializedPopulation, op.materializedOwner = population, owner
		scope.identity, scope.currentSchemaResult = identity, schema
		scope.input = input
	}()
	auth, err := e.authorizationLocked(op)
	if err == nil {
		err = scope.captureIdentityLocked(auth)
	}
	if err == nil {
		// Preserve operation settings, but never borrow caller catalog authority
		// for the population owner's restricted reference path.
		candidate := input
		candidate.Regclass, candidate.Regtype, candidate.Regrole = nil, nil, nil
		candidate.Operator, candidate.Regproc, candidate.ACLItem = nil, nil, nil
		candidate.TypeDiagnosticName = nil
		captureEnumOperationSafety(op, &candidate)
		err = captureRetainedDefinitionInput(auth.catalog, viewOf(d),
			func() error { return e.captureElementInput(auth, op, &candidate) },
			func(typ *sqltype.Descriptor) error { return e.captureTypeDiagnosticInput(auth, op, &candidate, typ) })
		if err == nil {
			scope.input = candidate
		}
	}
	if err != nil {
		e.mu.Unlock()
		return nil, err
	}
	root, source, err := e.prepareStoredQueryLocked(scope, ref, viewOf(d), d.Owner, queryRowMark{}, nil, nil)
	if err != nil {
		return nil, err
	}
	result, _, err := finalizeQuery(op.Context(), root, source, nil)
	if err != nil {
		return nil, err
	}
	return result.(*finalRows).takeTableRows(op.Context())
}

type materializedReplacement struct {
	original    *catalog.Relation
	predecessor *relationStorage
	storage     *relationStorage
	generation  uint64
	length      int
	descriptor  *catalog.Relation
	ends        *deletionRoot
	concurrent  bool
	indexCount  int
}

func (e *Engine) stageMaterializedRows(ctx context.Context, s *CatalogStage, d *catalog.Relation, previous *relationStorage, rows [][]sqltype.Value, populated bool, tx ids.TxRef) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if s == nil || s.owner != e || s.lifecycle != catalogOpen || s.invalid != nil || s.materializedReplacement != nil || d == nil || d.RelationKind != catalog.MaterializedViewRelation || d.View == nil || s.Base.Get(d.Address()) != d || e.stageCatalogBaseLocked(s).Get(d.Address()) != d || previous == nil || e.stageStorageLocked(s, d.Address()) != previous || !populated && len(rows) != 0 {
		return fmt.Errorf("stuntgres: invalid materialized replacement stage")
	}
	if s.Base.Epoch() != e.catalog.state.Epoch() {
		return ids.ErrCatalogReplaced
	}
	if d.Revision() == ids.DefinitionRevision(^uint64(0)) || previous.generation == ^uint64(0) {
		return fmt.Errorf("stuntgres: materialized replacement generation exhausted")
	}
	for _, row := range rows {
		if len(row) != len(d.Columns) {
			return fmt.Errorf("stuntgres: invalid materialized row width")
		}
		for i, value := range row {
			if value.Type() != d.Columns[i].Usage.Type {
				return fmt.Errorf("stuntgres: invalid materialized value type")
			}
		}
	}
	versions, err := e.allocateRowVersionsLocked(rows, nil)
	if err != nil {
		return err
	}
	next := *d
	next.ObjectRevision++
	next.Populated = populated
	if err := s.replaceDescriptor(&next, false); err != nil {
		return err
	}
	s.materializedReplacement = &materializedReplacement{original: d, descriptor: &next, predecessor: previous, storage: &relationStorage{versions: versions, generation: previous.generation + 1}, generation: previous.generation, length: len(previous.versions), ends: previous.ends}
	for _, descriptor := range s.Base.Descriptors() {
		if index, ok := descriptor.(*catalog.Relation); ok && index.SQLIndex != nil && index.SQLIndex.Parent == d.Address() {
			s.materializedReplacement.indexCount++
		}
	}
	if s.indexSets == nil {
		s.indexSets = make(map[ids.ObjectAddress]int)
	}
	s.indexSets[d.Address()] = s.materializedReplacement.indexCount
	s.initialTx = tx
	return nil
}

func (s *CatalogStage) validateMaterializedReplacement() error {
	r := s.materializedReplacement
	if r == nil {
		return nil
	}
	if r.original == nil || r.descriptor == nil {
		return fmt.Errorf("stuntgres: invalid materialized replacement")
	}
	d, ok := s.Descriptors[r.original.Address()].(*catalog.Relation)
	if !ok || r.original.RelationKind != catalog.MaterializedViewRelation || s.Base.Get(r.original.Address()) != r.original || r.storage == nil || r.predecessor == nil || r.storage == r.predecessor || r.storage.ends != nil || r.storage.generation != r.generation+1 || r.generation == ^uint64(0) {
		return fmt.Errorf("stuntgres: invalid materialized replacement")
	}
	// This private constructor owns the shallow descriptor copy. No general
	// Replace/ALTER caller may attach arbitrary metadata to its storage authority.
	old := r.original
	if d != r.descriptor || d.Address() != old.Address() || d.Namespace() != old.Namespace() || d.Name() != old.Name() || d.Owner != old.Owner || d.RowType != old.RowType || d.View != old.View || d.RelationKind != catalog.MaterializedViewRelation || d.Revision() != old.Revision()+1 || len(d.Columns) != len(old.Columns) || len(d.Attributes) != len(old.Attributes) || len(d.Columns) != 0 && &d.Columns[0] != &old.Columns[0] || len(d.Attributes) != 0 && &d.Attributes[0] != &old.Attributes[0] || !d.Populated && len(r.storage.versions) != 0 {
		return fmt.Errorf("stuntgres: invalid materialized replacement definition")
	}
	if r.concurrent && !d.Populated {
		return fmt.Errorf("stuntgres: concurrent replacement is unpopulated")
	}
	for _, row := range r.storage.versions {
		if row.row == 0 || row.version == 0 || row.born != 0 || len(row.values) != len(d.Columns) {
			return fmt.Errorf("stuntgres: invalid materialized replacement row")
		}
		for i, v := range row.values {
			if v.Type() != d.Columns[i].Usage.Type {
				return fmt.Errorf("stuntgres: invalid materialized replacement value")
			}
		}
	}
	return nil
}

func (s *CatalogStage) materializedReplacementCurrent(snapshot *catalogState, storage *relationStorage) error {
	r := s.materializedReplacement
	if r == nil {
		return nil
	}
	if s.Descriptors[r.original.Address()] != r.descriptor {
		return CatalogConflict{}
	}
	if snapshot.Epoch() != s.Base.Epoch() || snapshot.Get(r.original.Address()) != r.original || storage != r.predecessor || r.predecessor.generation != r.generation || len(r.predecessor.versions) != r.length || r.predecessor.ends != r.ends {
		return CatalogConflict{}
	}
	// The effective diff depends on every eligible key; root publication also
	// depends on ineligible indexes. Fence the exact original complete set,
	// without turning a replacement into a REINDEX maintenance intent.
	count := 0
	for address, descriptor := range snapshot.Descriptors() {
		index, ok := descriptor.(*catalog.Relation)
		if !ok || index.SQLIndex == nil || index.SQLIndex.Parent != r.original.Address() {
			continue
		}
		if s.Base.Get(address) != descriptor {
			return CatalogConflict{}
		}
		count++
	}
	if count != r.indexCount {
		return CatalogConflict{}
	}
	return nil
}
