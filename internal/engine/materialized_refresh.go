package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func materializedRefreshAccess(auth AuthorizationView, ref ids.ObjectRef) error {
	d, ok := auth.Catalog.Relation(ref.Address)
	if !ok || ref.Domain() != auth.Owner.Domain || ref.Epoch != auth.Catalog.Epoch() {
		return ids.ErrCatalogReplaced
	}
	if d.RelationKind != catalog.TableRelation && d.RelationKind != catalog.PartitionedRelation && d.RelationKind != catalog.MaterializedViewRelation {
		return sqlError("42809", fmt.Sprintf(`"%s" is not a table or materialized view`, d.Name()))
	}
	if err := auth.Check(access.Requirement{Object: ref, Privileges: authorization.MaintainPrivilege}); err != nil {
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
		observed = auth.Catalog.MustRelation(ref.Address)
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
		current, _ = auth.Catalog.Relation(ref.Address)
	}
	if err == nil && (s.txRef.Epoch() != e.catalog.state.Epoch() || !materializedRefreshCompatible(observed, current)) {
		err = ids.ErrCatalogReplaced
	}
	if err == nil {
		err = materializedRefreshAccess(auth, ref)
	}
	if err == nil && stmt.Concurrent.Character != 0 {
		err = materializedConcurrentAdmission(auth.Catalog, current, stmt.NoData)
	}
	if err == nil {
		s.lockedRef, s.lockedDescriptor, s.lockedStorage = ref, current, e.authorizationStorageLocked(auth, ref.Address)
	}
	e.mu.Unlock()
	if err != nil {
		return err
	}
	if !stmt.NoData {
		s.materialized().refresh = true
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
	d := caller.Catalog.MustRelation(ref.Address)
	if d != scope.lockedDescriptor || e.authorizationStorageLocked(caller, ref.Address) != scope.lockedStorage {
		e.mu.Unlock()
		return Result{}, ids.ErrCatalogReplaced
	}
	concurrent := stmt.Concurrent.Character != 0
	var indices []*catalog.Relation
	var previous storage.Relation
	var oldEntries []storage.IndexEntry
	var ownRows []storage.RowVersion
	var ownDeletes *visibility.PrivateDeletes
	frontier := e.visibility.CommitSeq()
	if concurrent {
		indices = materializedEligibleIndexes(caller.Catalog, d)
		previous = *scope.lockedStorage
		var root *storage.IndexRoot
		if len(indices) != 0 {
			root = e.storage.IndexRoots[indices[0].Address()]
			if scope.owner.catalog != nil && scope.owner.catalog.indexesOrZero().uniqueRoots[indices[0].Address()] != nil {
				root = scope.owner.catalog.indexesOrZero().uniqueRoots[indices[0].Address()]
			}
		}
		if root == nil {
			e.mu.Unlock()
			return Result{}, fmt.Errorf("stuntgres: concurrent materialized index root is missing")
		}
		oldEntries = root.Entries
		ownRows, ownDeletes = scope.columnProjectionRoots(d.Address())
	}
	stage := e.BeginCatalogStage(caller.Catalog)
	stage.authorizeLocked(caller, scope.op.session)
	stage.querySources = scope.materializedOrZero().sources
	stage.materialized().sourceView = scope.materializedOrZero().sourceView
	stage.indexes().output = scope.output
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
			versions := make([]storage.RowVersion, 0, len(previous.Versions)+len(ownRows))
			for _, row := range previous.Versions {
				if visibility.LiveVersion(row, frontier, previous.Ends, ownDeletes) {
					versions = append(versions, row)
				}
			}
			for _, row := range ownRows {
				if visibility.LiveVersion(row, frontier, nil, ownDeletes) {
					versions = append(versions, row)
				}
			}
			previous.Versions, previous.Ends = versions, nil
			evaluation := EvalContext{evalStatement: evalStatement{Context: scope.op.Context(), statementScope: scope, Input: scope.input, Output: scope.output, catalog: stage.Base}, evalFrame: evalFrame{Scope: 1, Frame: 1}}
			oldEntries, err = buildIndexEntries(&evaluation, d, indices[0].SQLIndex, &previous, nil)
			if err != nil {
				return Result{}, err
			}
		}
		rows, err = materializedConcurrentRows(scope.op.Context(), caller.Catalog, d, &previous, frontier, oldEntries, indices, rows, scope.output)
		if err != nil {
			return Result{}, err
		}
	}
	if err := e.stageMaterializedRows(scope.op.Context(), stage, d, scope.lockedStorage, rows, !stmt.NoData, scope.txRef); err != nil {
		return Result{}, err
	}
	stage.materialized().replacement.concurrent = concurrent
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
	population, owner := op.replayOrZero().materializedPopulation, op.replayOrZero().materializedOwner
	identity, schema := scope.identity, scope.inquiry.currentSchemaResult
	input := scope.input
	op.replay().materializedPopulation, op.replay().materializedOwner = true, d.Owner
	scope.inquiry.currentSchemaResult = sqltype.Value{}
	defer func() {
		op.replay().materializedPopulation, op.replay().materializedOwner = population, owner
		scope.identity, scope.inquiry.currentSchemaResult = identity, schema
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
		candidate.FTS = nil
		candidate.TypeDiagnosticName = nil
		captureEnumOperationSafety(op, &candidate)
		err = captureRetainedDefinitionInput(auth.Catalog, viewOf(d),
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
	predecessor *storage.Relation
	storage     *storage.Relation
	generation  uint64
	length      int
	descriptor  *catalog.Relation
	ends        *storage.DeletionRoot
	concurrent  bool
	indexCount  int
}

func (e *Engine) stageMaterializedRows(ctx context.Context, s *CatalogStage, d *catalog.Relation, previous *storage.Relation, rows [][]sqltype.Value, populated bool, tx ids.TxRef) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if s == nil || s.owner != e || s.lifecycle != catalogOpen || s.invalid != nil || s.materializedOrZero().replacement != nil || d == nil || d.RelationKind != catalog.MaterializedViewRelation || d.View == nil || s.Base.Get(d.Address()) != d || e.stageCatalogBaseLocked(s).Get(d.Address()) != d || previous == nil || e.stageStorageLocked(s, d.Address()) != previous || !populated && len(rows) != 0 {
		return fmt.Errorf("stuntgres: invalid materialized replacement stage")
	}
	if s.Base.Epoch() != e.catalog.state.Epoch() {
		return ids.ErrCatalogReplaced
	}
	if d.Revision() == ids.DefinitionRevision(^uint64(0)) || previous.Generation == ^uint64(0) {
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
	versions, err := e.storage.AllocateRowVersionsLocked(rows, nil)
	if err != nil {
		return err
	}
	next := *d
	next.ObjectRevision++
	next.Populated = populated
	if err := s.replaceDescriptor(&next, false); err != nil {
		return err
	}
	s.materialized().replacement = &materializedReplacement{original: d, descriptor: &next, predecessor: previous, storage: &storage.Relation{Versions: versions, Generation: previous.Generation + 1}, generation: previous.Generation, length: len(previous.Versions), ends: previous.Ends}
	for _, descriptor := range s.Base.Descriptors() {
		if index, ok := descriptor.(*catalog.Relation); ok && index.SQLIndex != nil && index.SQLIndex.Parent == d.Address() {
			s.materialized().replacement.indexCount++
		}
	}
	if s.indexesOrZero().sets == nil {
		s.indexes().sets = make(map[ids.ObjectAddress]int)
	}
	s.indexes().sets[d.Address()] = s.materializedOrZero().replacement.indexCount
	s.initialTx = tx
	return nil
}

func (s *CatalogStage) validateMaterializedReplacement() error {
	r := s.materializedOrZero().replacement
	if r == nil {
		return nil
	}
	if r.original == nil || r.descriptor == nil {
		return fmt.Errorf("stuntgres: invalid materialized replacement")
	}
	d, ok := s.Get(r.original.Address()).(*catalog.Relation)
	if !ok || r.original.RelationKind != catalog.MaterializedViewRelation || s.Base.Get(r.original.Address()) != r.original || r.storage == nil || r.predecessor == nil || r.storage == r.predecessor || r.storage.Ends != nil || r.storage.Generation != r.generation+1 || r.generation == ^uint64(0) {
		return fmt.Errorf("stuntgres: invalid materialized replacement")
	}
	// This private constructor owns the shallow descriptor copy. No general
	// Replace/ALTER caller may attach arbitrary metadata to its storage authority.
	old := r.original
	if d != r.descriptor || d.Address() != old.Address() || d.Namespace() != old.Namespace() || d.Name() != old.Name() || d.Owner != old.Owner || d.RowType != old.RowType || d.View != old.View || d.RelationKind != catalog.MaterializedViewRelation || d.Revision() != old.Revision()+1 || len(d.Columns) != len(old.Columns) || len(d.Attributes) != len(old.Attributes) || len(d.Columns) != 0 && &d.Columns[0] != &old.Columns[0] || len(d.Attributes) != 0 && &d.Attributes[0] != &old.Attributes[0] || !d.Populated && len(r.storage.Versions) != 0 {
		return fmt.Errorf("stuntgres: invalid materialized replacement definition")
	}
	if r.concurrent && !d.Populated {
		return fmt.Errorf("stuntgres: concurrent replacement is unpopulated")
	}
	for _, row := range r.storage.Versions {
		if row.Row == 0 || row.Version == 0 || row.Born != 0 || len(row.Values) != len(d.Columns) {
			return fmt.Errorf("stuntgres: invalid materialized replacement row")
		}
		for i, v := range row.Values {
			if v.Type() != d.Columns[i].Usage.Type {
				return fmt.Errorf("stuntgres: invalid materialized replacement value")
			}
		}
	}
	return nil
}

func (s *CatalogStage) materializedReplacementCurrent(snapshot *catalog.State, stored *storage.Relation) error {
	r := s.materializedOrZero().replacement
	if r == nil {
		return nil
	}
	if s.Get(r.original.Address()) != r.descriptor {
		return catalog.Conflict{}
	}
	if snapshot.Epoch() != s.Base.Epoch() || snapshot.Get(r.original.Address()) != r.original || stored != r.predecessor || r.predecessor.Generation != r.generation || len(r.predecessor.Versions) != r.length || r.predecessor.Ends != r.ends {
		return catalog.Conflict{}
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
			return catalog.Conflict{}
		}
		count++
	}
	if count != r.indexCount {
		return catalog.Conflict{}
	}
	return nil
}

// materializedRefreshScope is the statement's REFRESH MATERIALIZED VIEW slot:
// the refresh flag, the captured source descriptors and their visibility data.
type materializedRefreshScope struct {
	refresh    bool
	sources    []catalog.Descriptor
	sourceView *visibility.Data
}

// materialized returns the statement's materialized slot, allocating it on first use.
func (s *StatementScope) materialized() *materializedRefreshScope {
	if s.materializedSlot == nil {
		s.materializedSlot = new(materializedRefreshScope)
	}
	return s.materializedSlot
}

// materializedOrZero reads the materialized slot; a nil slot reads as zero.
func (s *StatementScope) materializedOrZero() materializedRefreshScope {
	if s.materializedSlot == nil {
		return materializedRefreshScope{}
	}
	return *s.materializedSlot
}
