package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (s *StatementScope) acquireMaterializedRefresh(stmt *parser.RefreshMaterializedView) error {
	e := s.engine()
	e.mu.Lock()
	auth, err := e.authorizationLocked(s.op)
	var ref ids.ObjectRef
	var observed *catalog.Relation
	if err == nil {
		ref, err = e.resolveRelationReference(auth, stmt.Name, true)
	}
	if err == nil {
		err = MaterializedRefreshAccess(auth.View, auth, ref)
	}
	if err == nil {
		observed = auth.Catalog.MustRelation(ref.Address)
	}
	e.mu.Unlock()
	if err != nil {
		return err
	}
	policy := lock.Policy{TimeoutSlot: &s.op.LockWait, Timeout: s.op.LockTimeout, StatementDeadline: s.op.StatementDeadline, Source: s.op.SourceContext}
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
	if err == nil && (s.txRef.Epoch() != e.catalog.state.Epoch() || !MaterializedRefreshCompatible(observed, current)) {
		err = ids.ErrCatalogReplaced
	}
	if err == nil {
		err = MaterializedRefreshAccess(auth.View, auth, ref)
	}
	if err == nil && stmt.Concurrent.Character != 0 {
		err = MaterializedConcurrentAdmission(auth.Catalog, current, stmt.NoData)
	}
	if err == nil {
		s.lockedRef, s.lockedDescriptor, s.lockedStorage = ref, current, e.authorizationStorageLocked(auth, ref.Address)
	}
	e.mu.Unlock()
	if err != nil {
		return err
	}
	if !stmt.NoData {
		s.materialized().Refresh = true
		return s.acquireViewSources()
	}
	return nil
}

// Engine.mu is held on entry, and is consumed on every return.
func (e *Engine) refreshMaterializedLocked(scope *StatementScope, caller AuthorizationView, stmt *parser.RefreshMaterializedView, count int) (Result, error) {
	if count != 0 {
		e.mu.Unlock()
		return Result{}, fmt.Errorf("stuntgres: REFRESH MATERIALIZED VIEW takes no arguments")
	}
	ref := scope.lockedRef
	if err := MaterializedRefreshAccess(caller.View, caller, ref); err != nil {
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
		indices = MaterializedEligibleIndexes(caller.Catalog, d)
		previous = *scope.lockedStorage
		var root *storage.IndexRoot
		if len(indices) != 0 {
			root = e.storage.IndexRoots[indices[0].Address()]
			if scope.owner.Catalog != nil && index.JournalIndexesOrZero(scope.owner.Catalog).UniqueRoots[indices[0].Address()] != nil {
				root = index.JournalIndexesOrZero(scope.owner.Catalog).UniqueRoots[indices[0].Address()]
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
	stage.authorizeLocked(caller, scope.op.Session)
	stage.querySources = scope.materializedOrZero().Sources
	stage.materialized().SourceView = scope.materializedOrZero().SourceView
	stage.indexes().Output = scope.output
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
			evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: scope.op.Context(), Host: hostOf(scope), Input: scope.input, Output: scope.output, Catalog: stage.Base}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
			oldEntries, err = index.BuildIndexEntries(&evaluation, d, indices[0].SQLIndex, &previous, nil)
			if err != nil {
				return Result{}, err
			}
		}
		rows, err = MaterializedConcurrentRows(scope.op.Context(), caller.Catalog, d, &previous, frontier, oldEntries, indices, rows, scope.output)
		if err != nil {
			return Result{}, err
		}
	}
	if err := e.stageMaterializedRows(scope.op.Context(), stage, d, scope.lockedStorage, rows, !stmt.NoData, scope.txRef); err != nil {
		return Result{}, err
	}
	stage.materialized().Replacement.Concurrent = concurrent
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
	population, owner := operationReplayOrZero(op).materializedPopulation, operationReplayOrZero(op).materializedOwner
	identity, schema := scope.identity, scope.inquiry.currentSchemaResult
	input := scope.input
	operationReplay(op).materializedPopulation, operationReplay(op).materializedOwner = true, d.Owner
	scope.inquiry.currentSchemaResult = sqltype.Value{}
	defer func() {
		operationReplay(op).materializedPopulation, operationReplay(op).materializedOwner = population, owner
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
		err = stored.CaptureRetainedInput(auth.Catalog, stored.ViewOf(d),
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
	root, source, err := e.prepareStoredQueryLocked(scope, ref, stored.ViewOf(d), d.Owner, plan.RowMark{}, nil, nil)
	if err != nil {
		return nil, err
	}
	result, _, err := exec.Finalize(op.Context(), root, source, nil)
	if err != nil {
		return nil, err
	}
	return result.(*exec.FinalRows).TakeTableRows(op.Context())
}

func (e *Engine) stageMaterializedRows(ctx context.Context, s *CatalogStage, d *catalog.Relation, previous *storage.Relation, rows [][]sqltype.Value, populated bool, tx ids.TxRef) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if s == nil || s.owner != e || s.lifecycle != catalogOpen || s.invalid != nil || s.materializedOrZero().Replacement != nil || d == nil || d.RelationKind != catalog.MaterializedViewRelation || d.View == nil || s.Base.Get(d.Address()) != d || e.stageCatalogBaseLocked(s).Get(d.Address()) != d || previous == nil || e.stageStorageLocked(s, d.Address()) != previous || !populated && len(rows) != 0 {
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
	s.materialized().Replacement = &materializedReplacement{Original: d, Descriptor: &next, Predecessor: previous, Storage: &storage.Relation{Versions: versions, Generation: previous.Generation + 1}, Generation: previous.Generation, Length: len(previous.Versions), Ends: previous.Ends}
	for _, descriptor := range s.Base.Descriptors() {
		if index, ok := descriptor.(*catalog.Relation); ok && index.SQLIndex != nil && index.SQLIndex.Parent == d.Address() {
			s.materialized().Replacement.IndexCount++
		}
	}
	if s.indexesOrZero().Sets == nil {
		s.indexes().Sets = make(map[ids.ObjectAddress]int)
	}
	s.indexes().Sets[d.Address()] = s.materializedOrZero().Replacement.IndexCount
	s.initialTx = tx
	return nil
}

func (s *CatalogStage) validateMaterializedReplacement() error {
	r := s.materializedOrZero().Replacement
	if r == nil {
		return nil
	}
	if r.Original == nil || r.Descriptor == nil {
		return fmt.Errorf("stuntgres: invalid materialized replacement")
	}
	d, ok := s.Get(r.Original.Address()).(*catalog.Relation)
	if !ok || r.Original.RelationKind != catalog.MaterializedViewRelation || s.Base.Get(r.Original.Address()) != r.Original || r.Storage == nil || r.Predecessor == nil || r.Storage == r.Predecessor || r.Storage.Ends != nil || r.Storage.Generation != r.Generation+1 || r.Generation == ^uint64(0) {
		return fmt.Errorf("stuntgres: invalid materialized replacement")
	}
	// This private constructor owns the shallow descriptor copy. No general
	// Replace/ALTER caller may attach arbitrary metadata to its storage authority.
	old := r.Original
	if d != r.Descriptor || d.Address() != old.Address() || d.Namespace() != old.Namespace() || d.Name() != old.Name() || d.Owner != old.Owner || d.RowType != old.RowType || d.View != old.View || d.RelationKind != catalog.MaterializedViewRelation || d.Revision() != old.Revision()+1 || len(d.Columns) != len(old.Columns) || len(d.Attributes) != len(old.Attributes) || len(d.Columns) != 0 && &d.Columns[0] != &old.Columns[0] || len(d.Attributes) != 0 && &d.Attributes[0] != &old.Attributes[0] || !d.Populated && len(r.Storage.Versions) != 0 {
		return fmt.Errorf("stuntgres: invalid materialized replacement definition")
	}
	if r.Concurrent && !d.Populated {
		return fmt.Errorf("stuntgres: concurrent replacement is unpopulated")
	}
	for _, row := range r.Storage.Versions {
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
	r := s.materializedOrZero().Replacement
	if r == nil {
		return nil
	}
	if s.Get(r.Original.Address()) != r.Descriptor {
		return catalog.Conflict{}
	}
	if snapshot.Epoch() != s.Base.Epoch() || snapshot.Get(r.Original.Address()) != r.Original || stored != r.Predecessor || r.Predecessor.Generation != r.Generation || len(r.Predecessor.Versions) != r.Length || r.Predecessor.Ends != r.Ends {
		return catalog.Conflict{}
	}
	// The effective diff depends on every eligible key; root publication also
	// depends on ineligible indexes. Fence the exact original complete set,
	// without turning a replacement into a REINDEX maintenance intent.
	count := 0
	for address, descriptor := range snapshot.Descriptors() {
		index, ok := descriptor.(*catalog.Relation)
		if !ok || index.SQLIndex == nil || index.SQLIndex.Parent != r.Original.Address() {
			continue
		}
		if s.Base.Get(address) != descriptor {
			return catalog.Conflict{}
		}
		count++
	}
	if count != r.IndexCount {
		return catalog.Conflict{}
	}
	return nil
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
