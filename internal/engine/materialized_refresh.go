package engine

import (
	"context"
	"fmt"
	"strconv"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func materializedRefreshCapability(s *parser.RefreshMaterializedView) error {
	if s.Concurrent.Character != 0 {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "concurrent materialized view refresh is unsupported", Position: strconv.Itoa(s.Concurrent.Character)}
	}
	return nil
}

func materializedRefreshAccess(auth AuthorizationView, ref ObjectRef) error {
	d, ok := auth.catalog.state.descriptors[ref.Address].(*relationDescriptor)
	if !ok || ref.owner != auth.engine.owner || ref.Epoch != auth.catalog.Epoch() {
		return ErrCatalogReplaced
	}
	if d.relationKind != tableRelation && d.relationKind != partitionedRelation && d.relationKind != materializedViewRelation {
		return sqlError("42809", fmt.Sprintf(`"%s" is not a table or materialized view`, d.name))
	}
	if err := auth.Check(AccessRequirement{Object: ref, Privileges: MaintainPrivilege}); err != nil {
		return err
	}
	if d.relationKind != materializedViewRelation {
		return sqlError("0A000", fmt.Sprintf(`"%s" is not a materialized view`, d.name))
	}
	return nil
}

func (s *StatementScope) acquireMaterializedRefresh(stmt *parser.RefreshMaterializedView) error {
	e := s.op.session.Engine()
	e.mu.Lock()
	auth, err := e.authorizationLocked(s.op)
	var ref ObjectRef
	var observed *relationDescriptor
	if err == nil {
		ref, err = e.resolveRelationReference(auth, stmt.Name, true)
	}
	if err == nil {
		err = materializedRefreshAccess(auth, ref)
	}
	if err == nil {
		observed = auth.catalog.state.descriptors[ref.Address].(*relationDescriptor)
	}
	e.mu.Unlock()
	if err != nil {
		return err
	}
	policy := relationLockPolicy{operation: s.op, timeout: s.op.lockTimeout, statementDeadline: s.op.statementDeadline, source: s.op.sourceContext}
	request, err := e.locks.startAcquire(s.op.Context(), s.ticket, lockResource{ObjectRef: ref}, accessExclusive, policy)
	if request != nil {
		if s.replacementRead {
			e.replacementMu.RUnlock()
			s.replacementRead = false
		}
		err = request.wait()
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
	current, _ := e.state.descriptors[ref.Address].(*relationDescriptor)
	if err == nil && (s.txRef.epoch != e.state.epoch || !materializedRefreshCompatible(observed, current)) {
		err = ErrCatalogReplaced
	}
	if err == nil {
		err = materializedRefreshAccess(auth, ref)
	}
	if err == nil {
		s.lockedRef, s.lockedDescriptor, s.lockedStorage = ref, current, e.storage[ref.Address]
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
// Capture its new store only at AX grant; never retry name resolution.
func materializedRefreshCompatible(old, current *relationDescriptor) bool {
	return current != nil && current.relationKind == materializedViewRelation &&
		current.address == old.address && current.namespace == old.namespace &&
		current.name == old.name && current.owner == old.owner &&
		current.view == old.view && current.rowType == old.rowType &&
		len(current.columns) == len(old.columns) && len(current.attributes) == len(old.attributes) &&
		(len(old.columns) == 0 || &current.columns[0] == &old.columns[0]) &&
		(len(old.attributes) == 0 || &current.attributes[0] == &old.attributes[0])
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
	d := caller.catalog.state.descriptors[ref.Address].(*relationDescriptor)
	if d != scope.lockedDescriptor || e.storage[ref.Address] != scope.lockedStorage {
		e.mu.Unlock()
		return Result{}, ErrCatalogReplaced
	}
	stage := e.BeginCatalogStage(caller.catalog)
	stage.authSession, stage.authRevision, stage.roleRevision = scope.op.session, caller.revision, e.roles.revision
	stage.querySources = scope.materializedSources
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
	if err := e.stageMaterializedRows(scope.op.Context(), stage, d, scope.lockedStorage, rows, !stmt.NoData, scope.txRef); err != nil {
		return Result{}, err
	}
	// Provider validation may replace the catalog. Exact stage fences, not this
	// admission lease, decide whether this original target can still publish.
	if scope.replacementRead {
		e.replacementMu.RUnlock()
		scope.replacementRead = false
	}
	prepared, err := e.ValidateCatalogStage(scope.op.Context(), stage)
	if err == nil {
		_, err = e.publishCatalogOperation(scope.op, prepared, PublicationToken{Epoch: stage.base.Epoch()})
	}
	if err == nil {
		scope.completed = true
	}
	return Result{}, err
}

// Population's effective principal and expression identity are restored before
// any stage validation/publication. No session settings or session_user change.
func (e *Engine) materializedRefreshRowsLocked(scope *StatementScope, ref ObjectRef, d *relationDescriptor) ([][]sqltype.Value, error) {
	op := scope.op
	population, owner := op.materializedPopulation, op.materializedOwner
	identity, schema := scope.identity, scope.currentSchemaResult
	op.materializedPopulation, op.materializedOwner = true, d.owner
	scope.currentSchemaResult = sqltype.Value{}
	defer func() {
		op.materializedPopulation, op.materializedOwner = population, owner
		scope.identity, scope.currentSchemaResult = identity, schema
	}()
	auth, err := e.authorizationLocked(op)
	if err == nil {
		err = scope.captureIdentityLocked(auth)
	}
	if err != nil {
		e.mu.Unlock()
		return nil, err
	}
	root, source, err := e.prepareStoredQueryLocked(scope, ref, d.view, d.owner, queryRowMark{}, nil)
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
	original    *relationDescriptor
	predecessor *relationStorage
	storage     *relationStorage
	generation  uint64
	length      int
	descriptor  *relationDescriptor
	ends        *deletionRoot
}

func (e *Engine) stageMaterializedRows(ctx context.Context, s *CatalogStage, d *relationDescriptor, previous *relationStorage, rows [][]sqltype.Value, populated bool, tx TxRef) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if s == nil || s.owner != e || s.lifecycle != catalogOpen || s.invalid != nil || s.materializedReplacement != nil || d == nil || d.relationKind != materializedViewRelation || d.view == nil || s.base.state.descriptors[d.address] != d || e.state.descriptors[d.address] != d || previous == nil || e.storage[d.address] != previous || !populated && len(rows) != 0 {
		return fmt.Errorf("stuntgres: invalid materialized replacement stage")
	}
	if s.base.Epoch() != e.state.epoch {
		return ErrCatalogReplaced
	}
	if d.revision == DefinitionRevision(^uint64(0)) || previous.generation == ^uint64(0) {
		return fmt.Errorf("stuntgres: materialized replacement generation exhausted")
	}
	for _, row := range rows {
		if len(row) != len(d.columns) {
			return fmt.Errorf("stuntgres: invalid materialized row width")
		}
		for i, value := range row {
			if value.Type() != d.columns[i].Usage.Type {
				return fmt.Errorf("stuntgres: invalid materialized value type")
			}
		}
	}
	versions, err := e.allocateRowVersionsLocked(rows, nil)
	if err != nil {
		return err
	}
	next := *d
	next.revision++
	next.populated = populated
	if err := s.replaceDescriptor(&next, false); err != nil {
		return err
	}
	s.materializedReplacement = &materializedReplacement{original: d, descriptor: &next, predecessor: previous, storage: &relationStorage{versions: versions, generation: previous.generation + 1}, generation: previous.generation, length: len(previous.versions), ends: previous.ends}
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
	d, ok := s.descriptors[r.original.address].(*relationDescriptor)
	if !ok || r.original.relationKind != materializedViewRelation || s.base.state.descriptors[r.original.address] != r.original || r.storage == nil || r.predecessor == nil || r.storage == r.predecessor || r.storage.ends != nil || r.storage.generation != r.generation+1 || r.generation == ^uint64(0) {
		return fmt.Errorf("stuntgres: invalid materialized replacement")
	}
	// This private constructor owns the shallow descriptor copy. No general
	// Replace/ALTER caller may attach arbitrary metadata to its storage authority.
	old := r.original
	if d != r.descriptor || d.address != old.address || d.namespace != old.namespace || d.name != old.name || d.owner != old.owner || d.rowType != old.rowType || d.view != old.view || d.relationKind != materializedViewRelation || d.revision != old.revision+1 || len(d.columns) != len(old.columns) || len(d.attributes) != len(old.attributes) || len(d.columns) != 0 && &d.columns[0] != &old.columns[0] || len(d.attributes) != 0 && &d.attributes[0] != &old.attributes[0] || !d.populated && len(r.storage.versions) != 0 {
		return fmt.Errorf("stuntgres: invalid materialized replacement definition")
	}
	for _, row := range r.storage.versions {
		if row.row == 0 || row.version == 0 || row.born != 0 || len(row.values) != len(d.columns) {
			return fmt.Errorf("stuntgres: invalid materialized replacement row")
		}
		for i, v := range row.values {
			if v.Type() != d.columns[i].Usage.Type {
				return fmt.Errorf("stuntgres: invalid materialized replacement value")
			}
		}
	}
	return nil
}

func (s *CatalogStage) materializedReplacementCurrent(catalog *catalogState, storage map[ObjectAddress]*relationStorage) error {
	r := s.materializedReplacement
	if r == nil {
		return nil
	}
	if s.descriptors[r.original.address] != r.descriptor {
		return CatalogConflict{}
	}
	if catalog.epoch != s.base.Epoch() || catalog.descriptors[r.original.address] != r.original || storage[r.original.address] != r.predecessor || r.predecessor.generation != r.generation || len(r.predecessor.versions) != r.length || r.predecessor.ends != r.ends {
		return CatalogConflict{}
	}
	return nil
}
