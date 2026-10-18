package engine

import (
	"context"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

// The caller retains replacementMu throughout, but providers must never run
// under the target catalog mutex. All effects belong to an isolated real session.
func (e *Engine) repairRemappedGenerationsLocked(ctx context.Context, next *catalogState, storage map[ids.ObjectAddress]*relationStorage, sequences map[ids.ObjectAddress]*sequenceGenerator, mapping map[ids.ObjectAddress]ids.ObjectAddress, op *Operation, indexes *indexSnapshot) (result error) {
	needed := indexes.needsOwnerRepair() || hasHashPartition(next)
	for source, target := range mapping {
		if source == target {
			continue
		}
		if d, ok := next.descriptors[target].(*relationDescriptor); ok {
			for _, g := range d.generations {
				needed = needed || generationHasTableOID(g.planned)
			}
		}
	}
	if !needed {
		return ctx.Err()
	}
	catalog, roles, frontier := e.catalog.state, e.roles, e.commitSeq
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		if result == nil && (e.catalog.state != catalog || e.roles != roles || e.commitSeq != frontier) {
			result = ErrCatalogReplaced
		}
	}()
	var stage *Engine
	if op == nil {
		stage = e.restoreStage()
	} else {
		stage = op.session.Engine()
	}
	candidate := cloneCatalog(next)
	stage.mu.Lock()
	_, err := stage.remapPreparedCatalog(candidate, e.roleRead(CatalogRead{e, next}, roles), stage.roleRead(CatalogRead{stage, stage.catalog.state}, stage.roles), nil)
	if err == nil {
		// remapCatalogExpressions targets the next epoch. Preserve final numeric
		// identities while projecting only engine/principal ownership.
		stage.catalog.state = cloneCatalog(stage.catalog.state)
		stage.catalog.state.epoch = candidate.epoch - 1
		err = stage.remapCatalogExpressions(candidate, nil)
	}
	if err == nil {
		stage.catalog.state = candidate
		stage.storage = storage
		stage.sequences = sequences
	}
	stage.mu.Unlock()
	if err != nil {
		return err
	}
	if op == nil {
		session, err := stage.StartSession(ctx, SessionStart{ID: 1, Authenticated: stage.authorizationDomain.Principal(10)})
		if err != nil {
			return err
		}
		defer func() { session.RequestEnd(CleanClose); _ = session.WaitEnd(context.Background()) }()
		op, err = session.Acquire(ctx)
		if err != nil {
			return err
		}
		defer op.Done()
	}
	scope, err := stage.StartStatement(op)
	if err != nil {
		return err
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
	evaluation := EvalContext{Context: ctx, Scope: 1, Frame: 1, statementScope: scope, Input: scope.input, Output: scope.output, catalog: CatalogRead{owner: stage, state: candidate}}
	changed := indexes.trackedGenerations(candidate)
	if err == nil {
		err = recomputeRemappedGenerations(ctx, candidate, storage, mapping, evaluation, changed)
	}
	var moved map[ids.ObjectAddress]bool
	if err == nil {
		moved, err = repairRemappedHashPartitions(ctx, &evaluation, storage, auth, indexes)
	}
	if err == nil {
		err = indexes.repair(ctx, &evaluation, storage, changed, moved, auth)
	}
	if err == nil {
		scope.completed = true
	}
	err = scope.Finish(err)
	if err != nil {
		return err
	}
	// Deferred reacquisition checks captured catalog, role incarnations and TEMP
	// membership atomically with the caller's remaining installation work.
	return ctx.Err()
}
