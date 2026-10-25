package engine

import (
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

// Engine.mu is held on entry and consumed on every return. The parent SUE grant
// is already admitted. All bound/provider work and manager waits run unlocked.
func attachPartitionLocked(e *Engine, scope *StatementScope, op *session.Operation, auth AuthorizationView, raw *parser.AlterTableAttachPartition, count int) (Result, error) {
	fail := func(err error) (Result, error) { e.Mu.Unlock(); return Result{}, err }
	if count != 0 {
		return fail(fmt.Errorf("stuntgres: got %d arguments, want 0", count))
	}
	if scope.LockedRef.Domain() == nil {
		return skipMissingRelationLocked(e, scope, op, raw.Name.Parts[len(raw.Name.Parts)-1].Text, true)
	}
	parent := scope.LockedDescriptor.(*catalog.Relation)
	if err := partition.CheckAttachTarget(parent, OperationReplayOrZero(op).PartitionAttach); err != nil {
		return fail(err)
	}
	lookupAuth, err := e.PartitionAttachAuthorization(op, auth)
	if err != nil {
		return fail(err)
	}
	if err = partitionAttachName(e, lookupAuth, raw.Name, scope.LockedRef); err != nil {
		return fail(err)
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.AuthorizeLocked(auth, op.Session)
	bound, err := partition.PrepareBounds(parent, raw.Bound)
	if err != nil {
		e.DiscardCatalogStage(stage)
		return fail(err)
	}
	pending := &pendingPartitionDefinition{Definition: partition.Definition{Parent: parent, Bound: bound, Raw: raw.Bound}, stage: stage, auth: auth}
	e.Mu.Unlock()
	defer func() { e.DiscardCatalogStage(pending.stage) }()
	if !bound.DefaultBound {
		evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: op.Context(), Host: HostOf(scope), Input: scope.Input, Output: scope.Output}}
		if err = partition.EvaluateBounds(op.Context(), &pending.Definition, partitionBoundBinder{scope: scope, pending: pending}, &evaluation); err != nil {
			return Result{}, err
		}
	}
	// PostgreSQL demands incumbent DEFAULT AX before candidate AX. Its proof
	// can elide descendant admission; candidate descendants always receive AX.
	var defaultScans []partition.AttachScan
	if !bound.DefaultBound && parent.Partition.DefaultChild != (ids.ObjectAddress{}) {
		ref := e.Catalog.Domain.Ref(auth.Catalog.Epoch(), parent.Partition.DefaultChild)
		if err = scope.AcquirePartitionLock(ref, lock.AccessExclusive); err != nil {
			return Result{}, err
		}
		catalog, root, err := partitionAttachRelation(e, scope, ref)
		if err != nil {
			return Result{}, err
		}
		defaultScans, err = partition.AttachFrontier(scope.Op.Context(), scope.Input, catalog, parent, root, bound, true, lockPartitionAttachChild(scope))
		if err != nil {
			return Result{}, err
		}
	}
	e.Mu.Lock()
	auth, err = e.StatementAuthorizationLocked(op, raw)
	if err != nil {
		e.Mu.Unlock()
		return Result{}, err
	}
	lookupAuth, err = e.PartitionAttachAuthorization(op, auth)
	if err != nil {
		e.Mu.Unlock()
		return Result{}, err
	}
	candidate, err := e.ResolveRelationReference(lookupAuth, raw.Partition, true)
	if err == nil {
		err = lookupAuth.Check(access.Requirement{Object: candidate, OwnerOnly: true, OwnerNoun: "table"})
	}
	e.Mu.Unlock()
	if err != nil {
		return Result{}, err
	}
	if err = scope.AcquirePartitionLock(candidate, lock.AccessExclusive); err != nil {
		return Result{}, err
	}
	catalog, child, err := partitionAttachRelation(e, scope, candidate)
	if err != nil {
		return Result{}, err
	}
	if err = partition.AttachEligibility(catalog, parent, child); err != nil {
		return Result{}, err
	}
	if err = scope.AcquirePartitionDescendants(candidate, lock.AccessExclusive); err != nil {
		return Result{}, err
	}

	// Capture the actual grant-time catalog, not the pre-wait index/provider set.
	// Evaluated datums survive this refresh and are never transformed twice.
	e.Mu.Lock()
	auth, err = e.StatementAuthorizationLocked(op, raw)
	if err == nil {
		lookupAuth, err = e.PartitionAttachAuthorization(op, auth)
	}
	if err == nil {
		err = partitionAttachName(e, lookupAuth, raw.Name, scope.LockedRef)
	}
	if err == nil {
		err = partitionAttachName(e, lookupAuth, raw.Partition, candidate)
	}
	if err == nil {
		err = lookupAuth.Check(access.Requirement{Object: scope.LockedRef, OwnerOnly: true, OwnerNoun: "table"})
	}
	if err == nil {
		err = lookupAuth.Check(access.Requirement{Object: candidate, OwnerOnly: true, OwnerNoun: "table"})
	}
	if err == nil {
		err = scope.ValidateLockedRelation(e)
	}
	if err != nil {
		e.Mu.Unlock()
		return Result{}, err
	}
	if auth.Catalog.Get(parent.Address()) != parent {
		e.Mu.Unlock()
		return Result{}, ids.ErrCatalogReplaced
	}
	child = auth.Catalog.MustRelation(candidate.Address)
	e.DiscardCatalogStage(pending.stage)
	stage = e.BeginCatalogStage(auth.Catalog)
	stage.AuthorizeLocked(auth, op.Session)
	stage.Indexes().Output = scope.Output
	pending.stage, pending.auth, pending.Child = stage, auth, child
	for address := range scope.PartitionRelations {
		stage.DomainBases = append(stage.DomainBases, auth.Catalog.Get(address))
	}
	stage.DomainBases = append(stage.DomainBases, parent, child)
	e.Mu.Unlock()
	if err = partition.AttachEligibility(auth.Catalog, parent, child); err != nil {
		return Result{}, err
	}
	next, err := partition.AttachSchema(scope.Op.Context(), auth.Catalog, parent, child)
	if err != nil {
		return Result{}, err
	}
	pending.Child = next
	if err = partition.ValidateBounds(scope.Op.Context(), scope.Input, &pending.Definition); err != nil {
		return Result{}, err
	}
	if err = partition.CheckDefaultConflict(auth.Catalog, parent, bound, child.Name(), raw.Bound.Pos); err != nil {
		return Result{}, err
	}
	if !bound.DefaultBound {
		if err = partition.CheckOverlap(scope.Op.Context(), scope.Input, pending.stage.Base, &pending.Definition); err != nil {
			return Result{}, err
		}
	}
	candidateScans, err := partition.AttachFrontier(scope.Op.Context(), scope.Input, auth.Catalog, parent, child, bound, false, nil)
	if err != nil {
		return Result{}, err
	}
	jobs := append(defaultScans, candidateScans...)
	if err = partitionAttachCapture(e, scope, stage, auth.Catalog, child, jobs); err != nil {
		return Result{}, err
	}
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: scope.Op.Context(), Host: HostOf(scope), Input: scope.Input, Output: scope.Output}}
	if err = partition.ValidateAttachRows(scope.Op.Context(), &evaluation, scope.PartitionsOrZero().ValidationView, auth.Catalog, parent, bound, jobs); err != nil {
		return Result{}, err
	}
	changed, next, err := partition.AttachDescriptors(parent, next, bound)
	if err != nil {
		return Result{}, err
	}
	if err = stage.Replace(changed); err != nil {
		return Result{}, err
	}
	if err = stage.Replace(next); err != nil {
		return Result{}, err
	}
	e.Mu.Lock()
	err = partition.StageIndexChildren(TableHost{DDLHost{E: e, Op: op, CatalogStage: stage}}, parent, child.Address(), true)
	e.Mu.Unlock()
	if err != nil {
		return Result{}, err
	}
	if _, err = e.ValidateCatalogStage(op.Context(), stage); err != nil {
		return Result{}, err
	}
	// The canonical overlay builds real populated key roots before returning;
	// implicit Finish and explicit COMMIT/savepoints own their publication.
	err = e.StageCatalogOverlay(scope, stage, scope.Owner.Catalog.Clone())
	return Result{}, err
}

func partitionAttachName(e *Engine, auth AuthorizationView, name parser.ObjectName, held ids.ObjectRef) error {
	current, err := e.ResolveRelationReference(auth, name, true)
	if err != nil || current != held {
		return ids.ErrCatalogReplaced
	}
	return nil
}

func partitionAttachRelation(e *Engine, scope *StatementScope, ref ids.ObjectRef) (catalog.Read, *catalog.Relation, error) {
	e.Mu.Lock()
	defer e.Mu.Unlock()
	view := e.PrivateCatalogLocked(scope.Owner.Catalog)
	d, ok := view.Relation(ref.Address)
	if !ok || ref.Domain() != e.Catalog.Domain || ref.Epoch != view.Epoch() || ref.Epoch != scope.TxRef.Epoch() {
		return catalog.Read{}, nil, ids.ErrCatalogReplaced
	}
	return view, d, nil
}

// Private import executes the same utility against captured definitions. Only
// its authenticated bootstrap may check the independently owned attachments.
func (e *Engine) PartitionAttachAuthorization(op *session.Operation, auth AuthorizationView) (AuthorizationView, error) {
	if !OperationReplayOrZero(op).PartitionAttach {
		return auth, nil
	}
	if op.Self != op || op.Session == nil || EngineOf(op.Session) != e || e.ImportTargets == nil || auth.Owner != &e.Catalog.Owner || auth.Session != op.Session || auth.Catalog.Owner() == nil || auth.Catalog.Owner().Domain != e.Catalog.Domain || auth.Catalog.State() != e.Catalog.State {
		return AuthorizationView{}, fmt.Errorf("stuntgres: invalid trusted restore partition attachment authority")
	}
	if err := e.ValidateOperation(op); err != nil {
		return AuthorizationView{}, err
	}
	bootstrap, err := auth.Roles.Descriptor(auth.State.Authenticated)
	if err != nil || !bootstrap.Superuser {
		return AuthorizationView{}, fmt.Errorf("stuntgres: invalid trusted restore partition attachment authority")
	}
	auth.State.CurrentUser = bootstrap.Ref
	return auth, nil
}
