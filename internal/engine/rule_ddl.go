package engine

import (
	"fmt"

	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
)

// executeRule runs CREATE, ALTER and DROP RULE: planRule decides the change
// under Engine.mu, and publishRelationChangeLocked stages and publishes it.
func (e *Engine) executeRule(scope *StatementScope, stmt parser.Statement, count int) (Result, error) {
	e.mu.Lock()
	fail := func(err error) (Result, error) { e.mu.Unlock(); return Result{}, err }
	if count != 0 {
		return fail(fmt.Errorf("stuntgres: got %d arguments, want 0", count))
	}
	auth, err := e.statementAuthorizationLocked(scope.op, stmt)
	if err != nil {
		return fail(err)
	}
	change, err := planRule(ddlHost{e: e, op: scope.op}, auth, stmt, scope.lockedRef, scope.input)
	if err != nil {
		return fail(err)
	}
	return e.publishRelationChangeLocked(scope, auth, change)
}

// publishRelationChangeLocked finishes a planned RULE or POLICY change.
// Engine.mu is held on entry and released on every return. A skip ends in its
// fenced NOTICE; otherwise the change is staged with its query sources and,
// after the unlock, replaced, validated and published against the epoch of
// the relation reference.
func (e *Engine) publishRelationChangeLocked(scope *StatementScope, auth AuthorizationView, change view.RelationChange) (Result, error) {
	if change.Skip != "" {
		return e.finishPolicyNoticeLocked(scope, change.Skip)
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.authorizeLocked(auth, scope.op.Session)
	stage.querySources = append(stage.querySources, change.Sources...)
	e.mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	if scope.replacementRead {
		e.replacementMu.RUnlock()
		scope.replacementRead = false
	}
	err := stage.Replace(change.Next)
	if err == nil {
		var prepared *PreparedCatalogPublication
		prepared, err = e.ValidateCatalogStage(scope.op.Context(), stage)
		if err == nil {
			_, err = e.publishCatalogOperation(scope.op, scope, prepared, PublicationToken{Epoch: change.Ref.Epoch})
		}
	}
	if err != nil {
		return Result{}, err
	}
	scope.completed = true
	return Result{}, nil
}
