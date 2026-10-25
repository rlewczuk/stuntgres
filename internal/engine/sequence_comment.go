package engine

import (
	"fmt"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/parser"
)

func (e *Engine) executeSequenceComment(scope *StatementScope, s *parser.Comment, count int) (Result, error) {
	if count != 0 {
		return Result{}, fmt.Errorf("stuntgres: COMMENT takes no arguments")
	}
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(scope.Op)
	var ref ids.ObjectRef
	if err == nil {
		ref, err = e.ResolveRelationReference(auth, s.Name, true)
	}
	if err == nil {
		err = sequence.CheckKind(auth.Catalog.MustRelation(ref.Address))
	}
	if err == nil {
		err = auth.Check(access.Requirement{Object: ref, OwnerOnly: true})
	}
	e.Mu.Unlock()
	if err != nil {
		return Result{}, err
	}
	if err = scope.AcquireSequence(ref, lock.ShareUpdateExclusive, true); err != nil {
		return Result{}, err
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err = e.AuthorizationLocked(scope.Op)
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
	next.ObjectRevision, err = e.Catalog.RevisionLocked()
	if err != nil {
		return Result{}, err
	}
	next.Comment = s.Text
	if s.Null {
		next.Comment = ""
	}
	changes := scope.Owner.Catalog.Clone()
	e.CatalogChangeLocked(changes, ref.Address, &next)
	scope.CatalogAuthority = auth.CatalogAuthorityLocked(ref, e.Catalog.State.Get(ref.Address))
	scope.Catalog = changes
	return Result{}, nil
}
