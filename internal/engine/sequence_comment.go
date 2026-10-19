package engine

import (
	"fmt"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/parser"
)

func (e *Engine) executeSequenceComment(scope *StatementScope, s *parser.Comment, count int) (Result, error) {
	if count != 0 {
		return Result{}, fmt.Errorf("stuntgres: COMMENT takes no arguments")
	}
	e.mu.Lock()
	auth, err := e.authorizationLocked(scope.op)
	var ref ids.ObjectRef
	if err == nil {
		ref, err = e.resolveRelationReference(auth, s.Name, true)
	}
	if err == nil {
		err = sequenceKind(auth.Catalog.MustRelation(ref.Address))
	}
	if err == nil {
		err = auth.Check(access.Requirement{Object: ref, OwnerOnly: true})
	}
	e.mu.Unlock()
	if err != nil {
		return Result{}, err
	}
	if err = scope.acquireSequence(ref, lock.ShareUpdateExclusive, true); err != nil {
		return Result{}, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	auth, err = e.authorizationLocked(scope.op)
	if err != nil {
		return Result{}, err
	}
	if err = auth.Check(access.Requirement{Object: ref, OwnerOnly: true}); err != nil {
		return Result{}, err
	}
	old, ok := auth.Catalog.Relation(ref.Address)
	if !ok {
		return Result{}, ids.ErrCatalogReplaced
	}
	next := *old
	next.ObjectRevision, err = e.catalog.revisionLocked()
	if err != nil {
		return Result{}, err
	}
	next.Comment = s.Text
	if s.Null {
		next.Comment = ""
	}
	changes := cloneCatalogChanges(scope.owner.catalog)
	e.catalogChangeLocked(changes, ref.Address, &next)
	scope.catalogAuthority = auth.catalogAuthorityLocked(ref, e.catalog.state.Get(ref.Address))
	scope.catalog = changes
	return Result{}, nil
}
