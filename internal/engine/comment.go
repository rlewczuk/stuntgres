package engine

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/role"
	"stuntgres/internal/parser"
)

// Comment reads immutable metadata from r, a view e owns, without applying an
// ACL to its text.
func (e *Engine) Comment(r catalog.Read, ref ids.ObjectRef) (string, bool, error) {
	if r.Owner() == nil {
		return "", false, fmt.Errorf("stuntgres: foreign catalog view")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.catalog.validateObjectRefLocked(r, ref); err != nil {
		return "", false, err
	}
	a := ref.Address
	sub := a.SubID
	a.SubID = 0
	switch r.Get(a).(type) {
	case *catalog.Namespace, *catalog.Type, *catalog.Relation:
	default:
		return "", false, fmt.Errorf("stuntgres: object class has no comment metadata")
	}
	text := pgcatalog.DescriptorComment(r.Get(a), sub)
	return text, text != "", nil
}

func (e *Engine) executeComment(scope *StatementScope, s *parser.Comment, count int) (Result, error) {
	if count != 0 {
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	op := scope.op
	e.mu.Lock()
	stage, err := e.stageCommentLocked(scope, s)
	e.mu.Unlock()
	if err != nil || stage == nil {
		return Result{}, err
	}
	defer e.DiscardCatalogStage(stage)
	prepared, err := e.ValidateCatalogStage(op.Context(), stage)
	if err != nil {
		return Result{}, err
	}
	_, err = e.publishCatalogOperation(op, scope, prepared, PublicationToken{Epoch: stage.Base.Epoch()})
	return Result{}, err
}

func (e *Engine) stageCommentLocked(scope *StatementScope, s *parser.Comment) (*CatalogStage, error) {
	op := scope.op
	if err := scope.validateLockedRelation(e); err != nil {
		return nil, err
	}
	auth, err := e.statementAuthorizationLocked(op, s)
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
		if err := publicationOperationLocked(e, op); err != nil {
			return nil, err
		}
		e.installOperationRolesLocked(op, roles)
		return nil, nil
	}
	updated, err := role.PlanObjectComment(auth.View, auth, e.catalog.Domain, s, scope.lockedRef, text, e.catalog.revisionLocked)
	if err != nil {
		return nil, err
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.authorizeLocked(auth, op.Session)
	if err := stage.Replace(updated); err != nil {
		return nil, err
	}
	return stage, nil
}
