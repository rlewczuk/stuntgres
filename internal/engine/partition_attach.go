package engine

import (
	"fmt"
	"slices"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
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
		op.Session.Mu.Lock()
		err := publicationOperationLocked(e, op)
		op.Session.Mu.Unlock()
		e.mu.Unlock()
		if err != nil {
			return Result{}, err
		}
		scope.completed = true
		op.EmitNotice(session.LevelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: fmt.Sprintf(`relation "%s" does not exist, skipping`, raw.Name.Parts[len(raw.Name.Parts)-1].Text)})
		return Result{}, nil
	}
	parent := scope.lockedDescriptor.(*catalog.Relation)
	if parent.Partition == nil {
		return fail(partitionAttachKindError(parent))
	}
	if parent.Partition.Strategy == parser.PartitionHash && !operationReplayOrZero(op).partitionAttach {
		return fail(sqlError("0A000", "hash partition attachment is unsupported"))
	}
	lookupAuth, err := e.partitionAttachAuthorization(op, auth)
	if err != nil {
		return fail(err)
	}
	if err = e.partitionAttachName(lookupAuth, raw.Name, scope.lockedRef); err != nil {
		return fail(err)
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.authorizeLocked(auth, op.Session)
	bound, err := preparePartitionBounds(parent, raw.Bound)
	if err != nil {
		e.DiscardCatalogStage(stage)
		return fail(err)
	}
	pending := &pendingPartitionDefinition{stage: stage, parent: parent, bound: bound, rawBounds: raw.Bound, auth: auth}
	e.mu.Unlock()
	defer func() { e.DiscardCatalogStage(pending.stage) }()
	if !bound.DefaultBound {
		evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: op.Context(), Host: hostOf(scope), Input: scope.input, Output: scope.output}}
		if err = pending.evaluateBounds(scope, &evaluation); err != nil {
			return Result{}, err
		}
	}
	// PostgreSQL demands incumbent DEFAULT AX before candidate AX. Its proof
	// can elide descendant admission; candidate descendants always receive AX.
	var defaultScans []partitionAttachScan
	if !bound.DefaultBound && parent.Partition.DefaultChild != (ids.ObjectAddress{}) {
		ref := e.catalog.Domain.Ref(auth.Catalog.Epoch(), parent.Partition.DefaultChild)
		if err = scope.acquirePartitionLock(ref, lock.AccessExclusive); err != nil {
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
		err = lookupAuth.Check(access.Requirement{Object: candidate, OwnerOnly: true, OwnerNoun: "table"})
	}
	e.mu.Unlock()
	if err != nil {
		return Result{}, err
	}
	if err = scope.acquirePartitionLock(candidate, lock.AccessExclusive); err != nil {
		return Result{}, err
	}
	catalog, child, err := e.partitionAttachRelation(scope, candidate)
	if err != nil {
		return Result{}, err
	}
	if err = partitionAttachEligibility(catalog, parent, child); err != nil {
		return Result{}, err
	}
	if err = scope.acquirePartitionDescendants(candidate, lock.AccessExclusive); err != nil {
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
		err = lookupAuth.Check(access.Requirement{Object: scope.lockedRef, OwnerOnly: true, OwnerNoun: "table"})
	}
	if err == nil {
		err = lookupAuth.Check(access.Requirement{Object: candidate, OwnerOnly: true, OwnerNoun: "table"})
	}
	if err == nil {
		err = scope.validateLockedRelation(e)
	}
	if err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	if auth.Catalog.Get(parent.Address()) != parent {
		e.mu.Unlock()
		return Result{}, ids.ErrCatalogReplaced
	}
	child = auth.Catalog.MustRelation(candidate.Address)
	e.DiscardCatalogStage(pending.stage)
	stage = e.BeginCatalogStage(auth.Catalog)
	stage.authorizeLocked(auth, op.Session)
	stage.indexes().output = scope.output
	pending.stage, pending.auth, pending.child = stage, auth, child
	for address := range scope.partitionRelations {
		stage.domainBases = append(stage.domainBases, auth.Catalog.Get(address))
	}
	stage.domainBases = append(stage.domainBases, parent, child)
	e.mu.Unlock()
	if err = partitionAttachEligibility(auth.Catalog, parent, child); err != nil {
		return Result{}, err
	}
	next, err := partitionAttachSchema(scope, auth.Catalog, parent, child)
	if err != nil {
		return Result{}, err
	}
	pending.child = next
	if err = pending.validateBounds(scope); err != nil {
		return Result{}, err
	}
	if bound.DefaultBound && parent.Partition.DefaultChild != (ids.ObjectAddress{}) {
		old := auth.Catalog.MustRelation(parent.Partition.DefaultChild)
		return Result{}, expr.ErrorAt("42P17", fmt.Sprintf(`partition "%s" conflicts with existing default partition "%s"`, child.Name(), old.Name()), raw.Bound.Pos)
	}
	if !bound.DefaultBound {
		if err = pending.checkOverlap(scope); err != nil {
			return Result{}, err
		}
	}
	candidateScans, err := scope.partitionAttachFrontier(auth.Catalog, parent, child, bound, false, false)
	if err != nil {
		return Result{}, err
	}
	jobs := append(defaultScans, candidateScans...)
	if err = e.partitionAttachCapture(scope, stage, auth.Catalog, child, jobs); err != nil {
		return Result{}, err
	}
	if err = partitionAttachValidateRows(scope, auth.Catalog, parent, bound, jobs); err != nil {
		return Result{}, err
	}
	if parent.Revision() == ids.DefinitionRevision(^uint64(0)) || next.Revision() == ids.DefinitionRevision(^uint64(0)) {
		return Result{}, fmt.Errorf("stuntgres: definition revision exhausted")
	}
	next.Membership = bound
	next.ObjectRevision++
	changed, definition := *parent, *parent.Partition
	definition.Children = append(slices.Clone(definition.Children), child.Address())
	if bound.DefaultBound {
		definition.DefaultChild = child.Address()
	}
	changed.Partition = &definition
	changed.ObjectRevision++
	if err = stage.Replace(&changed); err != nil {
		return Result{}, err
	}
	if err = stage.Replace(next); err != nil {
		return Result{}, err
	}
	e.mu.Lock()
	for _, index := range partitionIndexDeclarationOrder(auth.Catalog, parent) {
		current := e.stageRead(stage).MustRelation(child.Address())
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
	err = e.stageCatalogOverlay(scope, stage, scope.owner.Catalog.Clone())
	return Result{}, err
}

func (e *Engine) partitionAttachName(auth AuthorizationView, name parser.ObjectName, held ids.ObjectRef) error {
	current, err := e.resolveRelationReference(auth, name, true)
	if err != nil || current != held {
		return ids.ErrCatalogReplaced
	}
	return nil
}

func (e *Engine) partitionAttachRelation(scope *StatementScope, ref ids.ObjectRef) (catalog.Read, *catalog.Relation, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	view := e.privateCatalogLocked(scope.owner.Catalog)
	d, ok := view.Relation(ref.Address)
	if !ok || ref.Domain() != e.catalog.Domain || ref.Epoch != view.Epoch() || ref.Epoch != scope.txRef.Epoch() {
		return catalog.Read{}, nil, ids.ErrCatalogReplaced
	}
	return view, d, nil
}

// Private import executes the same utility against captured definitions. Only
// its authenticated bootstrap may check the independently owned attachments.
func (e *Engine) partitionAttachAuthorization(op *Operation, auth AuthorizationView) (AuthorizationView, error) {
	if !operationReplayOrZero(op).partitionAttach {
		return auth, nil
	}
	if op.Self != op || op.Session == nil || engineOf(op.Session) != e || e.importTargets == nil || auth.engine != e || auth.session != op.Session || auth.Catalog.Owner() == nil || auth.Catalog.Owner().Domain != e.catalog.Domain || auth.Catalog.State() != e.catalog.state {
		return AuthorizationView{}, fmt.Errorf("stuntgres: invalid trusted restore partition attachment authority")
	}
	if err := e.validateOperation(op); err != nil {
		return AuthorizationView{}, err
	}
	bootstrap, err := auth.Roles.Descriptor(auth.State.Authenticated)
	if err != nil || !bootstrap.Superuser {
		return AuthorizationView{}, fmt.Errorf("stuntgres: invalid trusted restore partition attachment authority")
	}
	auth.State.CurrentUser = bootstrap.Ref
	return auth, nil
}
