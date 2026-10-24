package engine

import (
	"fmt"

	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

// The class-specific target adapter uses the same role closure, grant-source,
// ACL transformation and transactional publication authorities as other GRANTs.
func (e *Engine) executeForeignGrant(scope *StatementScope, s *parser.ObjectGrant, count int) (Result, error) {
	if count != 0 {
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	op := scope.op
	auth, err := e.authorizationLocked(op)
	if err != nil {
		return Result{}, err
	}
	changes := scope.owner.Catalog.Clone()
	plan, warnings, err := fdw.PlanGrant(op.Context(), auth.View, s, changes.Descriptors, e.catalog.RevisionLocked)
	for _, warning := range warnings {
		op.EmitNotice(session.LevelWarning, warning)
	}
	if err != nil {
		return Result{}, err
	}
	for _, change := range plan {
		e.catalogChangeLocked(changes, change.Address, change.Object)
	}
	scope.catalog = changes
	scope.catalogAuthority = auth.catalogAuthorityLocked(ids.ObjectRef{}, nil)
	return Result{}, nil
}
