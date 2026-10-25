package engine

import (
	"fmt"
	"stuntgres/internal/engine/role"
	"stuntgres/internal/parser"
)

func (e *Engine) executeComment(scope *StatementScope, s *parser.Comment, count int) (Result, error) {
	if count != 0 {
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	op := scope.Op
	e.Mu.Lock()
	stage, err := e.stageCommentLocked(scope, s)
	e.Mu.Unlock()
	if err != nil || stage == nil {
		return Result{}, err
	}
	defer e.DiscardCatalogStage(stage)
	prepared, err := e.ValidateCatalogStage(op.Context(), stage)
	if err != nil {
		return Result{}, err
	}
	_, err = e.PublishCatalogOperation(op, scope, prepared, PublicationToken{Epoch: stage.Base.Epoch()})
	return Result{}, err
}

func (e *Engine) stageCommentLocked(scope *StatementScope, s *parser.Comment) (*CatalogStage, error) {
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
		if err := PublicationOperationLocked(e, op); err != nil {
			return nil, err
		}
		return nil, e.installOperationRolesLocked(op, roles)
	}
	updated, err := role.PlanObjectComment(auth.View, auth, e.Catalog.Domain, s, scope.LockedRef, text, e.Catalog.RevisionLocked)
	if err != nil {
		return nil, err
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.AuthorizeLocked(auth, op.Session)
	if err := stage.Replace(updated); err != nil {
		return nil, err
	}
	return stage, nil
}
