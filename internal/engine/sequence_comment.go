package engine

import (
	"fmt"
	"stuntgres/internal/parser"
)

func (e *Engine) executeSequenceComment(scope *StatementScope, s *parser.Comment, count int) (Result, error) {
	if count != 0 {
		return Result{}, fmt.Errorf("stuntgres: COMMENT takes no arguments")
	}
	e.mu.Lock()
	auth, err := e.authorizationLocked(scope.op)
	var ref ObjectRef
	if err == nil {
		ref, err = e.resolveRelationReference(auth, s.Name, true)
	}
	if err == nil {
		err = sequenceKind(auth.catalog.state.descriptors[ref.Address].(*relationDescriptor))
	}
	if err == nil {
		err = auth.Check(AccessRequirement{Object: ref, OwnerOnly: true})
	}
	e.mu.Unlock()
	if err != nil {
		return Result{}, err
	}
	if err = scope.acquireSequence(ref, shareUpdateExclusive, true); err != nil {
		return Result{}, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	auth, err = e.authorizationLocked(scope.op)
	if err != nil {
		return Result{}, err
	}
	if err = auth.Check(AccessRequirement{Object: ref, OwnerOnly: true}); err != nil {
		return Result{}, err
	}
	old, ok := auth.catalog.state.descriptors[ref.Address].(*relationDescriptor)
	if !ok {
		return Result{}, ErrCatalogReplaced
	}
	next := *old
	next.revision, err = e.revisionLocked()
	if err != nil {
		return Result{}, err
	}
	next.comment = s.Text
	if s.Null {
		next.comment = ""
	}
	changes := cloneCatalogChanges(scope.owner.catalog)
	e.catalogChangeLocked(changes, ref.Address, &next)
	scope.catalog = changes
	return Result{}, nil
}
