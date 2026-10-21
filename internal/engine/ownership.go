package engine

import (
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/role"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func (e *Engine) ownershipStage(auth AuthorizationView, op *session.Operation) *CatalogStage {
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.authorizeLocked(auth, op.Session)
	return stage
}
func (e *Engine) publishOwnership(scope *StatementScope, stage *CatalogStage) (Result, error) {
	defer e.DiscardCatalogStage(stage)
	if stage.DescriptorCount() == 0 && stage.RemovalCount() == 0 && stage.DefaultACLCount() == 0 && stage.roleCandidate == nil {
		if err := e.validateOperation(scope.op); err != nil {
			return Result{}, err
		}
		scope.completed = true
		return Result{}, scope.op.Context().Err()
	}
	prepared, err := e.ValidateCatalogStage(scope.op.Context(), stage)
	if err == nil {
		_, err = e.publishCatalogOperation(scope.op, scope, prepared, PublicationToken{Epoch: stage.Base.Epoch()})
	}
	if err == nil {
		scope.completed = true
	}
	return Result{}, err
}
func (e *Engine) executeRelationOwnership(scope *StatementScope, name parser.ObjectName, targetName string, identity bool, kind string, ifExists bool, count int) (Result, error) {
	if count != 0 {
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	e.mu.Lock()
	fail := func(err error) (Result, error) { e.mu.Unlock(); return Result{}, err }
	if err := scope.validateLockedRelation(e); err != nil {
		return fail(err)
	}
	auth, err := e.authorizationLocked(scope.op)
	if err != nil {
		return fail(err)
	}
	ref, err := e.resolveRelationReference(auth, name, true)
	if ifExists && table.RenameMissing(err) {
		e.mu.Unlock()
		scope.completed = true
		scope.op.EmitNotice(session.LevelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: fmt.Sprintf(`relation "%s" does not exist, skipping`, name.Parts[len(name.Parts)-1].Text)})
		return Result{}, scope.op.Context().Err()
	}
	if err != nil {
		return fail(err)
	}
	if ref != scope.lockedRef {
		return fail(ids.ErrCatalogReplaced)
	}
	old := auth.Catalog.MustRelation(ref.Address)
	if kind == "materialized view" {
		if err := AlterViewAuthority(auth.View, auth, ref, &parser.AlterView{Materialized: true, Action: "owner"}); err != nil {
			return fail(err)
		}
	}
	change, err := role.PlanRelationOwnership(auth.View, auth, old, ref, kind, targetName, identity)
	if err != nil {
		return fail(err)
	}
	if change.Object == nil {
		e.mu.Unlock()
		scope.completed = true
		return Result{}, scope.op.Context().Err()
	}
	stage := e.ownershipStage(auth, scope.op)
	err = role.StageOwnership(stage, old, change.Target)
	e.mu.Unlock()
	if err == nil {
		for _, a := range access.OwnershipRelationLocks(stage.Base, []ids.ObjectAddress{old.Address()}) {
			if a == old.Address() {
				continue
			}
			if err = scope.acquireSequence(e.catalog.Domain.Ref(stage.Base.Epoch(), a), lock.AccessExclusive, true); err != nil {
				break
			}
		}
	}
	if scope.replacementRead {
		e.replacementMu.RUnlock()
		scope.replacementRead = false
	}
	if err != nil {
		e.DiscardCatalogStage(stage)
		return Result{}, err
	}
	return e.publishOwnership(scope, stage)
}
func (e *Engine) executeOwnership(scope *StatementScope, s *parser.AlterOwner, count int) (Result, error) {
	name, identity := access.OwnershipRoleOperand(s.Target)
	if s.Kind == "database" {
		return e.executeDatabaseOwnership(scope, s, name, identity, count)
	}
	if s.Kind != "type" && s.Kind != "domain" {
		return e.executeRelationOwnership(scope, s.Name, name, identity, s.Kind, s.IfExists, count)
	}
	if count != 0 {
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	e.mu.Lock()
	fail := func(err error) (Result, error) { e.mu.Unlock(); return Result{}, err }
	auth, err := e.authorizationLocked(scope.op)
	if err != nil {
		return fail(err)
	}
	change, err := role.PlanTypeOwnership(ddlHost{e: e, op: scope.op}, auth, s, name, identity)
	if err != nil {
		return fail(err)
	}
	if change.Object == nil {
		e.mu.Unlock()
		scope.completed = true
		return Result{}, scope.op.Context().Err()
	}
	stage := e.ownershipStage(auth, scope.op)
	err = role.StageOwnership(stage, change.Object, change.Target)
	e.mu.Unlock()
	if err == nil {
		for _, a := range access.OwnershipRelationLocks(stage.Base, []ids.ObjectAddress{change.Object.Address()}) {
			if err = scope.acquireSequence(e.catalog.Domain.Ref(stage.Base.Epoch(), a), lock.AccessExclusive, true); err != nil {
				break
			}
		}
	}
	if scope.replacementRead {
		e.replacementMu.RUnlock()
		scope.replacementRead = false
	}
	if err != nil {
		e.DiscardCatalogStage(stage)
		return Result{}, err
	}
	return e.publishOwnership(scope, stage)
}
func (e *Engine) executeDatabaseOwnership(scope *StatementScope, s *parser.AlterOwner, name string, identity bool, count int) (Result, error) {
	if count != 0 {
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	e.mu.Lock()
	fail := func(err error) (Result, error) { e.mu.Unlock(); return Result{}, err }
	auth, err := e.authorizationLocked(scope.op)
	if err != nil {
		return fail(err)
	}
	change, err := role.PlanDatabaseOwnership(auth.View, auth, s, name, identity)
	if err != nil {
		return fail(err)
	}
	if change.Object == nil {
		e.mu.Unlock()
		scope.completed = true
		return Result{}, scope.op.Context().Err()
	}
	stage := e.ownershipStage(auth, scope.op)
	err = role.StageOwnership(stage, change.Object, change.Target)
	e.mu.Unlock()
	if err != nil {
		e.DiscardCatalogStage(stage)
		return Result{}, err
	}
	if scope.replacementRead {
		e.replacementMu.RUnlock()
		scope.replacementRead = false
	}
	return e.publishOwnership(scope, stage)
}
