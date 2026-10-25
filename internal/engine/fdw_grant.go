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
	e.Mu.Lock()
	defer e.Mu.Unlock()
	op := scope.Op
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		return Result{}, err
	}
	changes := scope.Owner.Catalog.Clone()
	plan, warnings, err := fdw.PlanGrant(op.Context(), auth.View, s, changes.Descriptors, e.Catalog.RevisionLocked)
	for _, warning := range warnings {
		op.EmitNotice(session.LevelWarning, warning)
	}
	if err != nil {
		return Result{}, err
	}
	for _, change := range plan {
		e.CatalogChangeLocked(changes, change.Address, change.Object)
	}
	scope.Catalog = changes
	scope.CatalogAuthority = auth.CatalogAuthorityLocked(ids.ObjectRef{}, nil)
	return Result{}, nil
}
