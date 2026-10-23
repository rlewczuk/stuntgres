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
func (e *Engine) executePolicy(scope *StatementScope, stmt parser.Statement, count int) (Result, error) {
	op := scope.op
	e.mu.Lock()
	fail := func(err error) (Result, error) { e.mu.Unlock(); return Result{}, err }
	if count != 0 {
		return fail(fmt.Errorf("stuntgres: got %d arguments, want 0", count))
	}
	auth, err := e.statementAuthorizationLocked(op, stmt)
	if err != nil {
		return fail(err)
	}
	if err = view.PolicyPreflight(op, auth.View, stmt, false); err != nil {
		return fail(err)
	}
	change, err := view.PlanPolicy(ddlHost{e: e, op: op}, auth, stmt, scope.lockedRef, scope.input)
	if err != nil {
		return fail(err)
	}
	return e.publishRelationChangeLocked(scope, auth, change, triggerReplacement{})
}

// Engine.mu is held on entry and released on every return. Even a missing-object
// success observes final cancellation/session/epoch fences before its notice.
func (e *Engine) finishPolicyNoticeLocked(scope *StatementScope, message string) (Result, error) {
	op := scope.op
	op.Session.Mu.Lock()
	err := publicationOperationLocked(e, op)
	op.Session.Mu.Unlock()
	if err == nil {
		err = op.Context().Err()
	}
	e.mu.Unlock()
	if err != nil {
		return Result{}, err
	}
	scope.completed = true
	op.EmitNotice(session.LevelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: message})
	return Result{}, nil
}
