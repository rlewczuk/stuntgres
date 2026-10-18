package engine

import (
	"fmt"
	"slices"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// Engine.mu is held on entry and consumed on every return. The parent SUE grant
// is already admitted. All bound/provider work and manager waits run unlocked.
func (e *Engine) attachPartitionLocked(scope *StatementScope, op *Operation, auth AuthorizationView, raw *parser.AlterTableAttachPartition, count int) (Result, error) {
	fail := func(err error) (Result, error) { e.mu.Unlock(); return Result{}, err }
	if count != 0 {
		return fail(fmt.Errorf("stuntgres: got %d arguments, want 0", count))
	}
	if scope.lockedRef.Domain() == nil {
		op.session.mu.Lock()
		err := publicationOperationLocked(e, op)
		op.session.mu.Unlock()
		e.mu.Unlock()
		if err != nil {
			return Result{}, err
		}
		scope.completed = true
		op.emitNotice(levelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: fmt.Sprintf(`relation "%s" does not exist, skipping`, raw.Name.Parts[len(raw.Name.Parts)-1].Text)})
		return Result{}, nil
	}
	parent := scope.lockedDescriptor.(*relationDescriptor)
	if parent.partition == nil {
		return fail(partitionAttachKindError(parent))
	}
	if parent.partition.strategy == parser.PartitionHash && !op.restorePartitionAttach {
		return fail(sqlError("0A000", "hash partition attachment is unsupported"))
	}
	lookupAuth, err := e.partitionAttachAuthorization(op, auth)
	if err != nil {
		return fail(err)
	}
	if err = e.partitionAttachName(lookupAuth, raw.Name, scope.lockedRef); err != nil {
		return fail(err)
	}
	stage := e.BeginCatalogStage(auth.catalog)
	stage.authorizeLocked(auth, op.session)
	bound, err := preparePartitionBounds(parent, raw.Bound)
	if err != nil {
		e.DiscardCatalogStage(stage)
		return fail(err)
	}
	pending := &pendingPartitionDefinition{stage: stage, parent: parent, bound: bound, rawBounds: raw.Bound, auth: auth}
	e.mu.Unlock()
	defer func() { e.DiscardCatalogStage(pending.stage) }()
	if !bound.defaultBound {
		evaluation := EvalContext{Context: op.Context(), statementScope: scope, Input: scope.input, Output: scope.output}
		if err = pending.evaluateBounds(scope, &evaluation); err != nil {
			return Result{}, err
		}
	}
	// PostgreSQL demands incumbent DEFAULT AX before candidate AX. Its proof
	// can elide descendant admission; candidate descendants always receive AX.
	var defaultScans []partitionAttachScan
	if !bound.defaultBound && parent.partition.defaultChild != (ids.ObjectAddress{}) {
		ref := e.catalog.domain.Ref(auth.catalog.Epoch(), parent.partition.defaultChild)
		if err = scope.acquirePartitionLock(ref, accessExclusive); err != nil {
			return Result{}, err
		}
		catalog, root, err := e.partitionAttachRelation(scope, ref)
		if err != nil {
			return Result{}, err
		}
		defaultScans, err = scope.partitionAttachFrontier(catalog, parent, root, bound, true, true)
		if err != nil {
			return Result{}, err
		}
	}
	e.mu.Lock()
	auth, err = e.statementAuthorizationLocked(op, raw)
	if err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	lookupAuth, err = e.partitionAttachAuthorization(op, auth)
	if err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	candidate, err := e.resolveRelationReference(lookupAuth, raw.Partition, true)
	if err == nil {
		err = lookupAuth.Check(AccessRequirement{Object: candidate, OwnerOnly: true, OwnerNoun: "table"})
	}
	e.mu.Unlock()
	if err != nil {
		return Result{}, err
	}
	if err = scope.acquirePartitionLock(candidate, accessExclusive); err != nil {
		return Result{}, err
	}
	catalog, child, err := e.partitionAttachRelation(scope, candidate)
	if err != nil {
		return Result{}, err
	}
	if err = partitionAttachEligibility(catalog, parent, child); err != nil {
		return Result{}, err
	}
	if err = scope.acquirePartitionDescendants(candidate, accessExclusive); err != nil {
		return Result{}, err
	}

	// Capture the actual grant-time catalog, not the pre-wait index/provider set.
	// Evaluated datums survive this refresh and are never transformed twice.
	e.mu.Lock()
	auth, err = e.statementAuthorizationLocked(op, raw)
	if err == nil {
		lookupAuth, err = e.partitionAttachAuthorization(op, auth)
	}
	if err == nil {
		err = e.partitionAttachName(lookupAuth, raw.Name, scope.lockedRef)
	}
	if err == nil {
		err = e.partitionAttachName(lookupAuth, raw.Partition, candidate)
	}
	if err == nil {
		err = lookupAuth.Check(AccessRequirement{Object: scope.lockedRef, OwnerOnly: true, OwnerNoun: "table"})
	}
	if err == nil {
		err = lookupAuth.Check(AccessRequirement{Object: candidate, OwnerOnly: true, OwnerNoun: "table"})
	}
	if err == nil {
		err = scope.validateLockedRelation(e)
	}
	if err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	if auth.catalog.state.descriptors[parent.address] != parent {
		e.mu.Unlock()
		return Result{}, ErrCatalogReplaced
	}
	child = auth.catalog.state.descriptors[candidate.Address].(*relationDescriptor)
	e.DiscardCatalogStage(pending.stage)
	stage = e.BeginCatalogStage(auth.catalog)
	stage.authorizeLocked(auth, op.session)
	stage.indexOutput = scope.output
	pending.stage, pending.auth, pending.child = stage, auth, child
	for address := range scope.partitionRelations {
		stage.domainBases = append(stage.domainBases, auth.catalog.state.descriptors[address])
	}
	stage.domainBases = append(stage.domainBases, parent, child)
	e.mu.Unlock()
	if err = partitionAttachEligibility(auth.catalog, parent, child); err != nil {
		return Result{}, err
	}
	next, err := partitionAttachSchema(scope, auth.catalog, parent, child)
	if err != nil {
		return Result{}, err
	}
	pending.child = next
	if err = pending.validateBounds(scope); err != nil {
		return Result{}, err
	}
	if bound.defaultBound && parent.partition.defaultChild != (ids.ObjectAddress{}) {
		old := auth.catalog.state.descriptors[parent.partition.defaultChild].(*relationDescriptor)
		return Result{}, expressionError("42P17", fmt.Sprintf(`partition "%s" conflicts with existing default partition "%s"`, child.name, old.name), raw.Bound.Pos)
	}
	if !bound.defaultBound {
		if err = pending.checkOverlap(scope); err != nil {
			return Result{}, err
		}
	}
	candidateScans, err := scope.partitionAttachFrontier(auth.catalog, parent, child, bound, false, false)
	if err != nil {
		return Result{}, err
	}
	jobs := append(defaultScans, candidateScans...)
	if err = e.partitionAttachCapture(scope, stage, auth.catalog, child, jobs); err != nil {
		return Result{}, err
	}
	if err = partitionAttachValidateRows(scope, auth.catalog, parent, bound, jobs); err != nil {
		return Result{}, err
	}
	if parent.revision == ids.DefinitionRevision(^uint64(0)) || next.revision == ids.DefinitionRevision(^uint64(0)) {
		return Result{}, fmt.Errorf("stuntgres: definition revision exhausted")
	}
	next.membership = bound
	next.revision++
	changed, definition := *parent, *parent.partition
	definition.children = append(slices.Clone(definition.children), child.address)
	if bound.defaultBound {
		definition.defaultChild = child.address
	}
	changed.partition = &definition
	changed.revision++
	if err = stage.Replace(&changed); err != nil {
		return Result{}, err
	}
	if err = stage.Replace(next); err != nil {
		return Result{}, err
	}
	e.mu.Lock()
	for _, index := range partitionIndexDeclarationOrder(auth.catalog, parent) {
		current := e.stageRead(stage).state.descriptors[child.address].(*relationDescriptor)
		if _, err = e.stagePartitionIndexChild(op, stage, parent, index, current, false); err != nil {
			break
		}
	}
	if err == nil {
		err = e.stagePartitionIndexValidity(stage, ids.ObjectAddress{})
	}
	e.mu.Unlock()
	if err != nil {
		return Result{}, err
	}
	if _, err = e.ValidateCatalogStage(op.Context(), stage); err != nil {
		return Result{}, err
	}
	// The canonical overlay builds real populated key roots before returning;
	// implicit Finish and explicit COMMIT/savepoints own their publication.
	err = e.stageCatalogOverlay(scope, stage, cloneCatalogChanges(scope.owner.catalog))
	return Result{}, err
}

func (e *Engine) partitionAttachName(auth AuthorizationView, name parser.ObjectName, held ids.ObjectRef) error {
	current, err := e.resolveRelationReference(auth, name, true)
	if err != nil || current != held {
		return ErrCatalogReplaced
	}
	return nil
}

func (e *Engine) partitionAttachRelation(scope *StatementScope, ref ids.ObjectRef) (CatalogRead, *relationDescriptor, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	catalog := e.privateCatalogLocked(scope.owner.catalog)
	d, ok := catalog.state.descriptors[ref.Address].(*relationDescriptor)
	if !ok || ref.Domain() != e.catalog.domain || ref.Epoch != catalog.Epoch() || ref.Epoch != scope.txRef.Epoch() {
		return CatalogRead{}, nil, ErrCatalogReplaced
	}
	return catalog, d, nil
}

// Private import executes the same utility against captured definitions. Only
// its authenticated bootstrap may check the independently owned attachments.
func (e *Engine) partitionAttachAuthorization(op *Operation, auth AuthorizationView) (AuthorizationView, error) {
	if !op.restorePartitionAttach {
		return auth, nil
	}
	if op.self != op || op.session == nil || op.session.Engine() != e || e.importTargets == nil || auth.engine != e || auth.session != op.session || auth.catalog.owner == nil || auth.catalog.owner.catalog.domain != e.catalog.domain || auth.catalog.state != e.catalog.state {
		return AuthorizationView{}, fmt.Errorf("stuntgres: invalid trusted restore partition attachment authority")
	}
	if err := e.validateOperation(op); err != nil {
		return AuthorizationView{}, err
	}
	bootstrap, err := auth.roles.Descriptor(auth.state.Authenticated)
	if err != nil || !bootstrap.Superuser {
		return AuthorizationView{}, fmt.Errorf("stuntgres: invalid trusted restore partition attachment authority")
	}
	auth.state.CurrentUser = bootstrap.Ref
	return auth, nil
}
