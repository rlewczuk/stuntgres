package engine

import (
	"fmt"

	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// executePolicy runs CREATE, ALTER and DROP POLICY: the preflight, then
// view.PlanPolicy under Engine.mu, then publishRelationChangeLocked.
func executePolicy(e *Engine, scope *StatementScope, stmt parser.Statement, count int) (Result, error) {
	op := scope.Op
	e.Mu.Lock()
	fail := func(err error) (Result, error) { e.Mu.Unlock(); return Result{}, err }
	if count != 0 {
		return fail(fmt.Errorf("stuntgres: got %d arguments, want 0", count))
	}
	auth, err := e.StatementAuthorizationLocked(op, stmt)
	if err != nil {
		return fail(err)
	}
	if err = view.PolicyPreflight(op, auth.View, stmt, false); err != nil {
		return fail(err)
	}
	change, err := view.PlanPolicy(ViewHost{TableHost{DDLHost{E: e, Op: op}}}, auth, stmt, scope.LockedRef, scope.Input)
	if err != nil {
		return fail(err)
	}
	return publishRelationChangeLocked(e, scope, auth, change)
}

// Engine.mu is held on entry and released on every return. Even a missing-object
// success observes final cancellation/session/epoch fences before its notice.
func finishPolicyNoticeLocked(e *Engine, scope *StatementScope, message string) (Result, error) {
	op := scope.Op
	op.Session.Mu.Lock()
	err := PublicationOperationLocked(e, op)
	op.Session.Mu.Unlock()
	if err == nil {
		err = op.Context().Err()
	}
	e.Mu.Unlock()
	if err != nil {
		return Result{}, err
	}
	scope.Completed = true
	op.EmitNotice(session.LevelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: message})
	return Result{}, nil
}
