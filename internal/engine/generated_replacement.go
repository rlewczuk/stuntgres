package engine

import (
	"context"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
)

// The caller retains replacementMu throughout, but providers must never run
// under the target catalog mutex. All effects belong to an isolated real session.
// A nonnil installedStage identifies the owner of the supplied map containers
// after repair; later container writes must use that store's locked methods.
// When no repair is needed, it returns nil and leaves the containers detached.
func (e *Engine) repairRemappedGenerationsLocked(ctx context.Context, next *catalog.State, relations map[ids.ObjectAddress]*storage.Relation, sequences map[ids.ObjectAddress]*storage.SequenceGenerator, mapping map[ids.ObjectAddress]ids.ObjectAddress, op *Operation, indexes *indexSnapshot) (installedStage *Engine, result error) {
	needed := indexes.needsOwnerRepair() || hasHashPartition(next)
	for source, target := range mapping {
		if source == target {
			continue
		}
		if d, ok := next.Relation(target); ok {
			for _, g := range d.ColumnMeta.Generations {
				needed = needed || generationHasTableOID(g.Planned)
			}
		}
	}
	if !needed {
		return installedStage, ctx.Err()
	}
	catalogSnapshot, roles, frontier := e.catalog.state, e.roles.published, e.visibility.CommitSeq()
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		if result == nil && (e.catalog.state != catalogSnapshot || e.roles.published != roles || e.visibility.CommitSeq() != frontier) {
			result = ids.ErrCatalogReplaced
		}
	}()
	var stage *Engine
	if op == nil {
		stage = e.restoreStage()
	} else {
		stage = op.session.Engine()
	}
	candidate := catalog.CloneState(next)
	stage.mu.Lock()
	_, err := stage.remapPreparedCatalog(candidate, e.roles.read(e.catalog.owner(), e.catalog.read(next), roles), stage.roles.read(stage.catalog.owner(), stage.catalog.read(stage.catalog.state), stage.roles.published), nil)
	if err == nil {
		// remapCatalogExpressions targets the next epoch. Preserve final numeric
		// identities while projecting only engine/principal ownership.
		stage.catalog.state = catalog.CloneState(stage.catalog.state)
		stage.catalog.state.SetEpoch(candidate.Epoch() - 1)
		err = stage.remapCatalogExpressions(candidate, nil)
	}
	if err == nil {
		stage.catalog.state = candidate
		stage.storage.ReplaceRelationsLocked(relations)
		stage.storage.ReplaceSequencesLocked(sequences)
		installedStage = stage
	}
	stage.mu.Unlock()
	if err != nil {
		return installedStage, err
	}
	if op == nil {
		session, err := stage.StartSession(ctx, SessionStart{ID: 1, Authenticated: stage.roles.domain.Principal(10)})
		if err != nil {
			return installedStage, err
		}
		defer func() { session.RequestEnd(CleanClose); _ = session.WaitEnd(context.Background()) }()
		op, err = session.Acquire(ctx)
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
			scope.input.TypeDiagnosticName, err = stage.captureTypeDiagnosticName(auth)
		}
		stage.mu.Unlock()
	}
	evaluation := EvalContext{EvalStatement: evalStatement{Context: ctx, Host: hostOf(scope), Input: scope.input, Output: scope.output, Catalog: stage.catalog.read(candidate)}, EvalFrame: evalFrame{Scope: 1, Frame: 1}}
	changed := indexes.trackedGenerations(candidate)
	if err == nil {
		err = recomputeRemappedGenerations(ctx, candidate, relations, mapping, evaluation, changed)
	}
	var moved map[ids.ObjectAddress]bool
	if err == nil {
		moved, err = stage.repairRemappedHashPartitions(ctx, &evaluation, auth, indexes)
	}
	if err == nil {
		err = indexes.repair(ctx, &evaluation, relations, changed, moved, auth)
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
