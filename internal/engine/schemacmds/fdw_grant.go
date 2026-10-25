package schemacmds

import (
	"fmt"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

// ExecuteForeignGrant runs GRANT and REVOKE on foreign-data wrappers and
// foreign servers.
//
// The class-specific target adapter uses the same role closure, grant-source,
// ACL transformation and transactional publication authorities as other GRANTs.
func ExecuteForeignGrant(e *core.Engine, scope *core.StatementScope, s *parser.ObjectGrant, count int) (core.Result, error) {
	if count != 0 {
		return core.Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	op := scope.Op
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		return core.Result{}, err
	}
	changes := scope.Owner.Catalog.Clone()
	plan, warnings, err := fdw.PlanGrant(op.Context(), auth.View, s, changes.Descriptors, e.Catalog.RevisionLocked)
	for _, warning := range warnings {
		op.EmitNotice(session.LevelWarning, warning)
	}
	if err != nil {
		return core.Result{}, err
	}
	for _, change := range plan {
		e.CatalogChangeLocked(changes, change.Address, change.Object)
	}
	scope.Catalog = changes
	scope.CatalogAuthority = auth.CatalogAuthorityLocked(ids.ObjectRef{}, nil)
	return core.Result{}, nil
}
