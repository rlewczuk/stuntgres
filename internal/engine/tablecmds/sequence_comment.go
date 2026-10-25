package tablecmds

import (
	"fmt"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/parser"
)

// ExecuteSequenceComment runs COMMENT ON SEQUENCE: it checks ownership, waits
// for the sequence lock, rechecks and stages the new comment.
func ExecuteSequenceComment(e *core.Engine, scope *core.StatementScope, s *parser.Comment, count int) (core.Result, error) {
	if count != 0 {
		return core.Result{}, fmt.Errorf("stuntgres: COMMENT takes no arguments")
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
		return core.Result{}, err
	}
	if err = scope.AcquireSequence(ref, lock.ShareUpdateExclusive, true); err != nil {
		return core.Result{}, err
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err = e.AuthorizationLocked(scope.Op)
	if err != nil {
		return core.Result{}, err
	}
	if err = auth.Check(access.Requirement{Object: ref, OwnerOnly: true}); err != nil {
		return core.Result{}, err
	}
	old, ok := auth.Catalog.Relation(ref.Address)
	if !ok {
		return core.Result{}, ids.ErrCatalogReplaced
	}
	next := *old
	next.ObjectRevision, err = e.Catalog.RevisionLocked()
	if err != nil {
		return core.Result{}, err
	}
	next.Comment = s.Text
	if s.Null {
		next.Comment = ""
	}
	changes := scope.Owner.Catalog.Clone()
	e.CatalogChangeLocked(changes, ref.Address, &next)
	scope.CatalogAuthority = auth.CatalogAuthorityLocked(ref, e.Catalog.State.Get(ref.Address))
	scope.Catalog = changes
	return core.Result{}, nil
}
