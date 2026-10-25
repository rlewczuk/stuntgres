package engine

import (
	"fmt"

	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
)

// executeTrigger runs CREATE [OR REPLACE], ALTER and DROP TRIGGER. Locks are
// acquired before Engine.mu: the statement admission, and for a rename the
// partition descendants in AccessExclusive. view.PlanTrigger decides the change
// under Engine.mu, and publishRelationChangeLocked stages and publishes it.
func (e *Engine) executeTrigger(scope *StatementScope, stmt parser.Statement, count int) (Result, error) {
	if count != 0 {
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	if s, ok := stmt.(*parser.AlterTrigger); ok && s.Action == "rename" {
		if err := scope.AcquirePartitionDescendants(scope.LockedRef, lock.AccessExclusive); err != nil {
			return Result{}, err
		}
	}
	e.Mu.Lock()
	fail := func(err error) (Result, error) { e.Mu.Unlock(); return Result{}, err }
	auth, err := e.StatementAuthorizationLocked(scope.Op, stmt)
	if err != nil {
		return fail(err)
	}
	change, err := view.PlanTrigger(DDLHost{E: e, Op: scope.Op}, auth, stmt, scope.LockedRef, scope.Input)
	if err != nil {
		return fail(err)
	}
	return e.publishRelationChangeLocked(scope, auth, change)
}
