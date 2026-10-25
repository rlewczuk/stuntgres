package engine

import (
	"context"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/session"
)

// validateRestoredSQLRoutines binds restored bodies as their owners after the
// whole replay. Only a terminal structured missing callable may defer binding;
// invocation must bind and check the declared result before opening producers.

func validateRestoredSQLRoutines(e *core.Engine, ctx context.Context, op *session.Operation) error {
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		return err
	}
	if index := auth.Catalog.State().ProcedureNames(); index == nil || len(index.DynamicNames) == 0 {
		return nil
	}
	input, _ := op.Session.Settings.Presentation()
	for _, descriptor := range auth.Catalog.Descriptors() {
		if err := ctx.Err(); err != nil {
			return err
		}
		if d, ok := descriptor.(*catalog.Procedure); ok && d.SQL != nil {
			auth.State.CurrentUser = d.Owner
			if d.SQL.Kind == proc.PLpgSQLBody {
				if err := e.CompilePLRoutineLocked(op, auth, d); err != nil {
					return err
				}
				continue
			}
			binding, _, bindErr := BindSQLRoutineLocked(e, op, auth, d, input, nil)
			if bindErr != nil && !binding.UnresolvedCallable {
				return bindErr
			}
		}
	}
	return nil
}
