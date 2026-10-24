package engine

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
	catalogSnapshot, roles, frontier := e.catalog.State, e.roles.Published, e.visibility.CommitSeq()
	oidFrontier := e.catalog.NextOID()
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		if result == nil && (e.catalog.State != catalogSnapshot || e.roles.Published != roles || e.visibility.CommitSeq() != frontier || e.catalog.NextOID() != oidFrontier) {
			result = ids.ErrCatalogReplaced
		}
	}()
	var stage *Engine
	if op == nil {
		stage = e.restoreStage()
	} else {
		stage = engineOf(op.Session)
	}
	candidate := catalog.CloneState(next)
	stage.mu.Lock()
	_, procedure, err := stage.remapPreparedCatalog(candidate, e.roles.Read(&e.catalog.Owner, e.catalog.Read(next), roles), stage.roles.PublishedRead(&stage.catalog.Owner, stage.catalog.Current()), nil)
	if err == nil {
		// ReownExpressions targets the next epoch. Preserve final numeric
		// identities while projecting only engine/principal ownership.
		stage.catalog.PublishLocked(catalog.CloneState(stage.catalog.State))
		stage.catalog.State.SetEpoch(candidate.Epoch() - 1)
		view := stage.catalog.Read(candidate)
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
			err = routine.RebindSQLDefinitions(candidate, remap)
		}
		if err == nil {
			err = stored.ReownExpressions(view, candidate, nil, false, remap)
		}
	}
	if err == nil {
		stage.catalog.PublishLocked(candidate)
		stage.storage.ReplaceRelationsLocked(relations)
		stage.storage.ReplaceSequencesLocked(sequences)
		installedStage = stage
	}
	stage.mu.Unlock()
	if err != nil {
		return installedStage, err
	}
	if op == nil {
		sess, err := stage.StartSession(ctx, session.SessionStart{ID: 1, Authenticated: stage.roles.Domain.Principal(10)})
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
	err = scope.gate(stmt)
	var auth AuthorizationView
	if err == nil {
		stage.mu.Lock()
		auth, err = stage.authorizationLocked(op)
		if err == nil {
			err = scope.captureIdentityLocked(auth)
			scope.routineAuth = auth
		}
		if err == nil {
			scope.input.TypeDiagnosticName, err = captureTypeDiagnosticName(auth)
		}
		stage.mu.Unlock()
	}
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: ctx, Host: hostOf(scope), Input: scope.input, Output: scope.output, Catalog: stage.catalog.Read(candidate)}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
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
		err = indexes.Repair(ctx, &evaluation, relations, changed, moved, auth.View, disclosureEnv(&auth))
	}
	if err == nil {
		scope.completed = true
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
	scope := scopeOf(host)
	if scope == nil {
		return nil
	}
	return func(historical *catalog.Relation) error {
		stage := scope.engine()
		stage.mu.Lock()
		err := stage.captureCheckCommentSnapshotLocked(scope, historical)
		if err == nil {
			err = stage.captureCheckVisibilitySnapshotLocked(scope, historical)
		}
		stage.mu.Unlock()
		return err
	}
}
