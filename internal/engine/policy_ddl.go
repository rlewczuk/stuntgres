package engine

import (
	"fmt"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/tablecmds"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// ExecutePolicy runs CREATE, ALTER and DROP POLICY: the preflight, then
// view.PlanPolicy under Engine.mu, then publishRelationChangeLocked.
func ExecutePolicy(e *core.Engine, scope *core.StatementScope, stmt parser.Statement, count int) (core.Result, error) {
	op := scope.Op
	e.Mu.Lock()
	fail := func(err error) (core.Result, error) { e.Mu.Unlock(); return core.Result{}, err }
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
	change, err := view.PlanPolicy(ViewHost{tablecmds.TableHost{core.DDLHost{E: e, Op: op}}}, auth, stmt, scope.LockedRef, scope.Input)
	if err != nil {
		return fail(err)
	}
	return publishRelationChangeLocked(e, scope, auth, change)
}

// Engine.mu is held on entry and released on every return. Even a missing-object
// success observes final cancellation/session/epoch fences before its notice.
func finishPolicyNoticeLocked(e *core.Engine, scope *core.StatementScope, message string) (core.Result, error) {
	op := scope.Op
	op.Session.Mu.Lock()
	err := core.PublicationOperationLocked(e, op)
	op.Session.Mu.Unlock()
	if err == nil {
		err = op.Context().Err()
	}
	e.Mu.Unlock()
	if err != nil {
		return core.Result{}, err
	}
	scope.Completed = true
	op.EmitNotice(session.LevelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: message})
	return core.Result{}, nil
}
