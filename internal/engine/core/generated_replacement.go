package core

import (
	"context"
	"maps"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// The caller holds ReplacementMu then Mu on entry and return. Demanded repair
// releases both around private-stage work, then rejects changed predecessors
// before the caller can attach live TEMP storage or install the candidate.
// A nonnil installedStage identifies the owner of the supplied map containers
// after repair; later container writes must use that store's locked methods.
// When no repair is needed, it returns nil and leaves the containers detached.
func (e *Engine) repairRemappedGenerationsLocked(ctx context.Context, next *catalog.State, relations map[ids.ObjectAddress]*storage.Relation, sequences map[ids.ObjectAddress]*storage.SequenceGenerator, mapping map[ids.ObjectAddress]ids.ObjectAddress, op *session.Operation, indexes *index.Snapshot) (installedStage *Engine, result error) {
	rangeValidation := len(mapping) != 0 && stored.HasAliasRangeTypes(next)
	needed := rangeValidation || indexes.NeedsOwnerRepair() || partition.HasHashPartition(next)
	for source, target := range mapping {
		if source == target {
			continue
		}
		if d, ok := next.Relation(target); ok {
			for _, g := range d.ColumnMeta.Generations {
				needed = needed || plan.GenerationHasTableOID(g.Planned)
			}
		}
	}
	if !needed {
		return installedStage, ctx.Err()
	}
	catalogSnapshot, roles, frontier := e.Catalog.State, e.RoleStore.Published, e.Visibility.CommitSeq()
	oidFrontier, revision := e.Catalog.NextOID(), e.Catalog.NextRevision()
	rowFrontier, versionFrontier := e.Storage.RowFrontier(), e.Storage.VersionFrontier()
	roleRevision, principal, membership := e.RoleStore.NextRevision(), e.RoleStore.NextPrincipal(), e.RoleStore.NextMembershipOID()
	sourceRoles := e.RoleStore.Read(&e.Catalog.Owner, e.Catalog.Read(next), roles)
	shapes := make(map[ids.ObjectAddress]storage.RowShape, len(e.Storage.Relations))
	for address, store := range e.Storage.Relations {
		shapes[address] = storage.RowShape{Original: store, Root: store.Versions, ObservedEnds: store.Ends, Generation: store.Generation}
	}
	roots := maps.Clone(e.Storage.IndexRoots)
	temporary := e.captureRepairTemporaryOwnersLocked()
	sourceContext, suppliedOperation := ctx, op != nil
	e.Mu.Unlock()
	e.ReplacementMu.Unlock()
	defer func() {
		e.ReplacementMu.Lock()
		e.Mu.Lock()
		if result != nil {
			return
		}
		result = sourceContext.Err()
		if result == nil && suppliedOperation {
			result = ctx.Err()
		}
		if result != nil {
			return
		}
		if e.Catalog.State != catalogSnapshot || e.RoleStore.Published != roles || e.Visibility.CommitSeq() != frontier ||
			e.Catalog.NextOID() != oidFrontier || e.Catalog.NextRevision() != revision ||
			e.Storage.RowFrontier() != rowFrontier || e.Storage.VersionFrontier() != versionFrontier ||
			e.RoleStore.NextRevision() != roleRevision || e.RoleStore.NextPrincipal() != principal || e.RoleStore.NextMembershipOID() != membership ||
			e.prepared.Len() != 0 || len(e.Storage.Relations) != len(shapes) || !maps.Equal(roots, e.Storage.IndexRoots) ||
			!e.repairTemporaryOwnersCurrentLocked(temporary) {
			result = ids.ErrCatalogReplaced
			return
		}
		for address, shape := range shapes {
			store := e.Storage.Relations[address]
			if store != shape.Original || store.Generation != shape.Generation || store.Ends != shape.ObservedEnds || !storage.SameVersionRoot(store.Versions, shape.Root) {
				result = ids.ErrCatalogReplaced
				return
			}
		}
		result = e.checkTemporaryRelationSourcesReplacementLocked()
		if result == nil {
			result = e.Storage.ReplacementCapacityLocked(relations, e.Catalog.State.Epoch(), e.Visibility.CommitSeq())
		}
	}()
	var stage *Engine
	if op == nil {
		stage = e.RestoreStage()
	} else {
		stage = EngineOf(op.Session)
	}
	candidate := catalog.CloneState(next)
	stage.Mu.Lock()
	_, procedure, err := stage.remapPreparedCatalog(candidate, sourceRoles, stage.RoleStore.PublishedRead(&stage.Catalog.Owner, stage.Catalog.Current()), nil)
	if err == nil {
		// ReownExpressions targets the next epoch. Preserve final numeric
		// identities while projecting only engine/principal ownership.
		stage.Catalog.PublishLocked(catalog.CloneState(stage.Catalog.State))
		stage.Catalog.State.SetEpoch(candidate.Epoch() - 1)
		view := stage.Catalog.Read(candidate)
		typ := func(old *sqltype.Descriptor) *sqltype.Descriptor {
			if old == nil {
				return nil
			}
			current, _ := view.LookupType(old.ID())
			return current
		}
		remap := stored.NewRoutineRemap(view, nil, typ, procedure, false)
		err = stored.ReownInstalledDomainChecks(view, candidate, nil, false, remap)
		if err == nil {
			err = routine.RebindRoutineDefinitions(candidate, remap)
		}
		if err == nil {
			err = stored.ReownExpressions(view, candidate, nil, false, remap)
		}
	}
	if err == nil {
		stage.Catalog.PublishLocked(candidate)
		stage.Storage.ReplaceRelationsLocked(relations)
		stage.Storage.ReplaceSequencesLocked(sequences)
		installedStage = stage
	}
	stage.Mu.Unlock()
	if err != nil {
		return installedStage, err
	}
	if op == nil {
		sess, err := stage.StartRestoreSession(ctx, stage.RoleStore.Domain.Principal(10))
		if err != nil {
			return installedStage, err
		}
		defer func() { sess.RequestEnd(session.CleanClose); _ = sess.WaitEnd(context.Background()) }()
		op, err = sess.Acquire(ctx)
		if err != nil {
			return installedStage, err
		}
		defer op.Done()
	}
	scope, err := stage.StartStatement(op)
	if err != nil {
		return installedStage, err
	}
	stmt := &parser.Insert{}
	err = scope.Gate(stmt)
	var auth AuthorizationView
	if err == nil {
		stage.Mu.Lock()
		auth, err = stage.AuthorizationLocked(op)
		if err == nil {
			err = scope.CaptureIdentityLocked(auth)
			scope.RoutineAuth = auth
		}
		if err == nil {
			scope.Input.TypeDiagnosticName, err = CaptureTypeDiagnosticName(auth)
		}
		stage.Mu.Unlock()
	}
	if err != nil {
		return installedStage, scope.Finish(err)
	}
	// Keep source ctx for admission above; every runtime consumer below borrows
	// exactly this statement's current effective child, including its cause.
	ctx = scope.ActivateRuntime(scope.Op.Context())
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: ctx, Host: HostOf(scope), Input: scope.Input, Output: scope.Output, Catalog: stage.Catalog.Read(candidate)}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
	if err == nil && rangeValidation {
		err = stored.ValidateRemappedRanges(ctx, candidate, relations)
	}
	changed := indexes.TrackedGenerations(candidate)
	if err == nil {
		err = table.RecomputeRemappedGenerations(ctx, candidate, relations, mapping, evaluation, changed, remapCheckCapture(evaluation.Host))
	}
	var moved map[ids.ObjectAddress]bool
	if err == nil {
		moved, err = stage.repairRemappedHashPartitions(ctx, &evaluation, auth, indexes)
	}
	if err == nil {
		err = indexes.Repair(ctx, &evaluation, relations, changed, moved, auth.View, DisclosureEnv(&auth))
	}
	if err == nil {
		scope.CompleteUtility()
	}
	err = scope.Finish(err)
	if err != nil {
		return installedStage, err
	}
	// Deferred reacquisition checks captured catalog, role incarnations and TEMP
	// membership atomically with the caller's remaining installation work.
	return installedStage, ctx.Err()
}

// remapCheckCapture captures the CHECK comment and visibility snapshots of a
// remapped relation for the statement behind host, in one Engine.mu window.
// It returns nil when host has no statement scope.
func remapCheckCapture(host exec.Host) func(*catalog.Relation) error {
	scope := ScopeOf(host)
	if scope == nil {
		return nil
	}
	return func(historical *catalog.Relation) error {
		stage := scope.Engine()
		stage.Mu.Lock()
		err := stage.CaptureCheckCommentSnapshotLocked(scope, historical)
		if err == nil {
			err = stage.CaptureCheckVisibilitySnapshotLocked(scope, historical)
		}
		stage.Mu.Unlock()
		return err
	}
}

// Catalog identity fences the immutable TEMP descriptors and namespace tokens.
// Session retirement and active namespace changes also need their own owner
// fence: RequestEnd may precede catalog cleanup. Mu guards the active namespace;
// Session.Mu guards State. Never retain sessionMu while taking Session.Mu.
type repairTemporaryOwner struct {
	session   *session.Session
	state     session.State
	namespace ids.ObjectAddress
}

func (e *Engine) captureRepairTemporaryOwnersLocked() map[ids.SessionToken]repairTemporaryOwner {
	var owners map[ids.SessionToken]repairTemporaryOwner
	for _, namespace := range e.Catalog.Current().TemporaryNamespaces() {
		if namespace.TemporaryToast {
			continue
		}
		if owners == nil {
			owners = make(map[ids.SessionToken]repairTemporaryOwner)
		}
		e.sessionMu.Lock()
		s := e.sessions[namespace.Temporary]
		e.sessionMu.Unlock()
		owner := repairTemporaryOwner{session: s}
		if s != nil {
			s.Mu.Lock()
			owner.state = s.State
			s.Mu.Unlock()
			owner.namespace = s.TemporaryNamespace
		}
		owners[namespace.Temporary] = owner
	}
	return owners
}

func (e *Engine) repairTemporaryOwnersCurrentLocked(owners map[ids.SessionToken]repairTemporaryOwner) bool {
	for token, owner := range owners {
		e.sessionMu.Lock()
		s := e.sessions[token]
		e.sessionMu.Unlock()
		if s != owner.session {
			return false
		}
		if s != nil {
			s.Mu.Lock()
			state := s.State
			s.Mu.Unlock()
			if state != owner.state || state != session.Live || s.TemporaryNamespace != owner.namespace {
				return false
			}
		}
	}
	return true
}
