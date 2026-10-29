package core

import (
	"context"
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

// The caller retains replacementMu throughout, but providers must never run
// under the target catalog mutex. All effects belong to an isolated real session.
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
	oidFrontier := e.Catalog.NextOID()
	e.Mu.Unlock()
	defer func() {
		e.Mu.Lock()
		if result == nil && (e.Catalog.State != catalogSnapshot || e.RoleStore.Published != roles || e.Visibility.CommitSeq() != frontier || e.Catalog.NextOID() != oidFrontier) {
			result = ids.ErrCatalogReplaced
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
	_, procedure, err := stage.remapPreparedCatalog(candidate, e.RoleStore.Read(&e.Catalog.Owner, e.Catalog.Read(next), roles), stage.RoleStore.PublishedRead(&stage.Catalog.Owner, stage.Catalog.Current()), nil)
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
		sess, err := stage.StartSession(ctx, session.SessionStart{ID: 1, Authenticated: stage.RoleStore.Domain.Principal(10)})
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
