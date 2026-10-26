package core

import (
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/role"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
)

func OwnershipStage(e *Engine, auth AuthorizationView, op *session.Operation) *CatalogStage {
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.AuthorizeLocked(auth, op.Session)
	return stage
}

func PublishOwnership(e *Engine, scope *StatementScope, stage *CatalogStage) (Result, error) {
	defer e.DiscardCatalogStage(stage)
	if stage.DescriptorCount() == 0 && stage.RemovalCount() == 0 && stage.DefaultACLCount() == 0 && stage.RoleCandidateRecord == nil {
		if err := e.validateOperation(scope.Op); err != nil {
			return Result{}, err
		}
		scope.Completed = true
		return Result{}, scope.Op.Context().Err()
	}
	prepared, err := e.ValidateCatalogStage(scope.Op.Context(), stage)
	if err == nil {
		_, err = e.PublishCatalogOperation(scope.Op, scope, prepared, PublicationToken{Epoch: stage.Base.Epoch()})
	}
	if err == nil {
		scope.Completed = true
	}
	return Result{}, err
}

func ExecuteRelationOwnership(e *Engine, scope *StatementScope, name parser.ObjectName, targetName string, identity bool, kind string, ifExists bool, count int) (Result, error) {
	if count != 0 {
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	e.Mu.Lock()
	fail := func(err error) (Result, error) { e.Mu.Unlock(); return Result{}, err }
	if err := scope.ValidateLockedRelation(e); err != nil {
		return fail(err)
	}
	auth, err := e.AuthorizationLocked(scope.Op)
	if err != nil {
		return fail(err)
	}
	ref, err := e.ResolveRelationReference(auth, name, true)
	if ifExists && table.RenameMissing(err) {
		e.Mu.Unlock()
		scope.Completed = true
		scope.Op.EmitNotice(session.LevelNotice, session.MissingRelation(name.Parts[len(name.Parts)-1].Text))
		return Result{}, scope.Op.Context().Err()
	}
	if err != nil {
		return fail(err)
	}
	if ref != scope.LockedRef {
		return fail(ids.ErrCatalogReplaced)
	}
	old := auth.Catalog.MustRelation(ref.Address)
	if kind == "materialized view" {
		if err := view.AlterViewAuthority(auth.View, auth, ref, &parser.AlterView{Materialized: true, Action: "owner"}); err != nil {
			return fail(err)
		}
	}
	change, err := role.PlanRelationOwnership(auth.View, auth, old, ref, kind, targetName, identity)
	if err != nil {
		return fail(err)
	}
	if change.Object == nil {
		e.Mu.Unlock()
		scope.Completed = true
		return Result{}, scope.Op.Context().Err()
	}
	stage := OwnershipStage(e, auth, scope.Op)
	err = role.StageOwnership(stage, old, change.Target)
	e.Mu.Unlock()
	if err == nil {
		for _, a := range access.OwnershipRelationLocks(stage.Base, []ids.ObjectAddress{old.Address()}, nil) {
			if a == old.Address() {
				continue
			}
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
