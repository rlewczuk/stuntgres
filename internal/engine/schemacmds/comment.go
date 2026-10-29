package schemacmds

import (
	"fmt"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/role"
	"stuntgres/internal/parser"
)

// ExecuteComment runs COMMENT ON for every object kind except sequences,
// whose comments are tablecmds.ExecuteSequenceComment's.
func ExecuteComment(e *core.Engine, scope *core.StatementScope, s *parser.Comment, count int) (core.Result, error) {
	if count != 0 {
		return core.Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	op := scope.Op
	e.Mu.Lock()
	stage, err := stageCommentLocked(e, scope, s)
	e.Mu.Unlock()
	if err != nil || stage == nil {
		return core.Result{}, err
	}
	defer e.DiscardCatalogStage(stage)
	prepared, err := e.ValidateCatalogStage(op.Context(), stage)
	if err != nil {
		return core.Result{}, err
	}
	_, err = e.PublishCatalogOperation(op, scope, prepared, core.PublicationToken{Epoch: stage.Base.Epoch()})
	return core.Result{}, err
}

func stageCommentLocked(e *core.Engine, scope *core.StatementScope, s *parser.Comment) (*core.CatalogStage, error) {
	op := scope.Op
	if err := scope.ValidateLockedRelation(e); err != nil {
		return nil, err
	}
	auth, err := e.StatementAuthorizationLocked(op, s)
	if err != nil {
		return nil, err
	}
	if err = op.Context().Err(); err != nil {
		return nil, err
	}
	text := s.Text
	if s.Null {
		text = ""
	}
	if s.Kind == parser.CommentRole {
		roles, err := role.PlanRoleComment(auth.View, s.Name.Parts[0].Text, text)
		if err != nil {
			return nil, err
		}
		op.Session.Mu.Lock()
		defer op.Session.Mu.Unlock()
		if err := core.PublicationOperationLocked(e, op); err != nil {
			return nil, err
		}
		return nil, core.InstallOperationRolesLocked(e, op, roles)
	}
	var target ids.ObjectAddress
	updated, err := role.PlanObjectComment(auth.View, auth, e.Catalog.Domain, s, scope.LockedRef, text, e.Catalog.RevisionLocked, &target)
	if err != nil {
		return nil, err
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.AuthorizeLocked(auth, op.Session)
	if err := stage.Replace(updated); err != nil {
		return nil, err
	}
	stage.RecordDDLCommand("COMMENT", target)
	return stage, nil
}
