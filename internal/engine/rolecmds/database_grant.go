package rolecmds

import (
	"fmt"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/role"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

// ExecuteDatabaseGrant runs GRANT and REVOKE ON DATABASE, one named database
// at a time, emitting the partial-grant warnings in statement order.
func ExecuteDatabaseGrant(e *core.Engine, scope *core.StatementScope, s *parser.ObjectGrant, count int) (core.Result, error) {
	if count != 0 {
		return core.Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	if s.Kind != "database" || len(s.Relations) == 0 || len(s.Schemas) != 0 {
		return core.Result{}, core.SQLError("0A000", "statement is not supported")
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	op := scope.Op
	v, err := e.AuthorizationLocked(op)
	if err != nil {
		return core.Result{}, err
	}
	var changes *session.Journal
	for i := range s.Relations {
		statement := *s
		statement.Relations = s.Relations[i : i+1]
		next, warnings, err := role.PlanDatabaseGrant(op.Context(), v.View, &statement, e.Catalog.RevisionLocked)
		for _, warning := range warnings {
			op.EmitNotice(session.LevelWarning, warning)
		}
		if err != nil {
			return core.Result{}, err
		}
		if next == nil {
			continue
		}
		if changes == nil {
			changes = scope.Owner.Catalog.Clone()
		}
		e.CatalogChangeLocked(changes, next.Address(), next)
	}
	if changes == nil {
		return core.Result{}, nil
	}
	scope.Catalog = changes
	scope.CatalogAuthority = v.CatalogAuthorityLocked(ids.ObjectRef{Address: v.Owner.DatabaseAddress()}, e.Catalog.State.Get(v.Owner.DatabaseAddress()))
	return core.Result{}, nil
}
