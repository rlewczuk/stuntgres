package engine

import (
	"fmt"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

func (e *Engine) executeDatabaseGrant(scope *StatementScope, s *parser.ObjectGrant, count int) (Result, error) {
	if count != 0 {
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	op := scope.op
	v, err := e.authorizationLocked(op)
	if err != nil {
		return Result{}, err
	}
	next, warnings, err := planDatabaseGrant(op.Context(), v.View, s, e.databaseName, e.catalog.revisionLocked)
	for _, warning := range warnings {
		op.EmitNotice(session.LevelWarning, warning)
	}
	if err != nil || next == nil {
		return Result{}, err
	}
	changes := scope.owner.Catalog.Clone()
	e.catalogChangeLocked(changes, next.Address(), next)
	scope.catalog = changes
	scope.catalogAuthority = v.catalogAuthorityLocked(ids.ObjectRef{Address: next.Address()}, e.catalog.state.Get(next.Address()))
	return Result{}, nil
}
