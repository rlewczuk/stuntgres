package engine

import (
	"fmt"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/role"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

func (e *Engine) executeDatabaseGrant(scope *StatementScope, s *parser.ObjectGrant, count int) (Result, error) {
	if count != 0 {
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	if s.Kind != "database" || len(s.Relations) == 0 || len(s.Schemas) != 0 {
		return Result{}, sqlError("0A000", "statement is not supported")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	op := scope.op
	v, err := e.authorizationLocked(op)
	if err != nil {
		return Result{}, err
	}
	var changes *session.Journal
	for i := range s.Relations {
		statement := *s
		statement.Relations = s.Relations[i : i+1]
		next, warnings, err := role.PlanDatabaseGrant(op.Context(), v.View, &statement, e.catalog.revisionLocked)
		for _, warning := range warnings {
			op.EmitNotice(session.LevelWarning, warning)
		}
		if err != nil {
			return Result{}, err
		}
		if next == nil {
			continue
		}
		if changes == nil {
			changes = scope.owner.Catalog.Clone()
		}
		e.catalogChangeLocked(changes, next.Address(), next)
	}
	if changes == nil {
		return Result{}, nil
	}
	scope.catalog = changes
	scope.catalogAuthority = v.catalogAuthorityLocked(ids.ObjectRef{Address: v.Owner.DatabaseAddress()}, e.catalog.state.Get(v.Owner.DatabaseAddress()))
	return Result{}, nil
}
