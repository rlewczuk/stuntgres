package engine

import (
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/role"
	"stuntgres/internal/parser"
)

func executeOwnership(e *Engine, scope *StatementScope, s *parser.AlterOwner, count int) (Result, error) {
	name, identity := access.OwnershipRoleOperand(s.Target)
	if s.Kind == "database" {
		return executeDatabaseOwnership(e, scope, s, name, identity, count)
	}
	if s.Kind != "type" && s.Kind != "domain" {
		return ExecuteRelationOwnership(e, scope, s.Name, name, identity, s.Kind, s.IfExists, count)
	}
	if count != 0 {
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	e.Mu.Lock()
	fail := func(err error) (Result, error) { e.Mu.Unlock(); return Result{}, err }
	auth, err := e.AuthorizationLocked(scope.Op)
	if err != nil {
		return fail(err)
	}
	change, err := role.PlanTypeOwnership(RoleHost{DDLHost{E: e, Op: scope.Op}}, auth, s, name, identity)
	if err != nil {
		return fail(err)
	}
	if change.Object == nil {
		e.Mu.Unlock()
		scope.Completed = true
		return Result{}, scope.Op.Context().Err()
	}
	stage := OwnershipStage(e, auth, scope.Op)
	err = role.StageOwnership(stage, change.Object, change.Target)
	e.Mu.Unlock()
	if err == nil {
		for _, a := range access.OwnershipRelationLocks(stage.Base, []ids.ObjectAddress{change.Object.Address()}, nil) {
			if err = scope.AcquireSequence(e.Catalog.Domain.Ref(stage.Base.Epoch(), a), lock.AccessExclusive, true); err != nil {
				break
			}
		}
	}
	if scope.ReplacementRead {
		e.ReplacementMu.RUnlock()
		scope.ReplacementRead = false
	}
	if err != nil {
		e.DiscardCatalogStage(stage)
		return Result{}, err
	}
	return PublishOwnership(e, scope, stage)
}
func executeDatabaseOwnership(e *Engine, scope *StatementScope, s *parser.AlterOwner, name string, identity bool, count int) (Result, error) {
	if count != 0 {
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	e.Mu.Lock()
	fail := func(err error) (Result, error) { e.Mu.Unlock(); return Result{}, err }
	auth, err := e.AuthorizationLocked(scope.Op)
	if err != nil {
		return fail(err)
	}
	change, err := role.PlanDatabaseOwnership(auth.View, auth, s, name, identity)
	if err != nil {
		return fail(err)
	}
	if change.Object == nil {
		e.Mu.Unlock()
		scope.Completed = true
		return Result{}, scope.Op.Context().Err()
	}
	stage := OwnershipStage(e, auth, scope.Op)
	err = role.StageOwnership(stage, change.Object, change.Target)
	e.Mu.Unlock()
	if err != nil {
		e.DiscardCatalogStage(stage)
		return Result{}, err
	}
	if scope.ReplacementRead {
		e.ReplacementMu.RUnlock()
		scope.ReplacementRead = false
	}
	return PublishOwnership(e, scope, stage)
}
