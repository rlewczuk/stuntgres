package engine

import (
	"context"
	"stuntgres/internal/parser"
)

// The caller retains replacementMu throughout, but providers must never run
// under the target catalog mutex. All effects belong to an isolated real session.
func (e *Engine) repairRemappedGenerationsLocked(ctx context.Context, next *catalogState, storage map[ObjectAddress]*relationStorage, sequences map[ObjectAddress]*sequenceGenerator, mapping map[ObjectAddress]ObjectAddress, op *Operation) (result error) {
	needed := false
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
	catalog, roles, frontier := e.state, e.roles, e.commitSeq
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		if result == nil && (e.state != catalog || e.roles != roles || e.commitSeq != frontier) {
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
	_, err := stage.remapPreparedCatalog(candidate, RoleRead{e, roles}, RoleRead{stage, stage.roles}, nil)
	if err == nil {
		// remapCatalogExpressions targets the next epoch. Preserve final numeric
		// identities while projecting only engine/principal ownership.
		stage.state = cloneCatalog(stage.state)
		stage.state.epoch = candidate.epoch - 1
		err = stage.remapCatalogExpressions(candidate, nil)
	}
	if err == nil {
		stage.state = candidate
		stage.storage = storage
		stage.sequences = sequences
	}
	stage.mu.Unlock()
	if err != nil {
		return err
	}
	if op == nil {
		session, err := stage.StartSession(ctx, SessionStart{ID: 1, Authenticated: PrincipalRef{stage, 10}})
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
	if err == nil {
		stage.mu.Lock()
		var auth AuthorizationView
		auth, err = stage.authorizationLocked(op)
		if err == nil {
			err = scope.captureIdentityLocked(auth)
		}
		stage.mu.Unlock()
	}
	if err == nil {
		err = recomputeRemappedGenerations(ctx, candidate, storage, mapping, EvalContext{Context: ctx, statementScope: scope, Input: scope.input, Output: scope.output})
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
