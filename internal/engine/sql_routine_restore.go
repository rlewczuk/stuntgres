package engine

import (
	"context"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/session"
)

// validateRestoredSQLRoutines binds restored bodies as their owners after the
// whole replay. Only a terminal structured missing callable may defer binding;
// invocation must bind and check the declared result before opening producers.

func (e *Engine) validateRestoredSQLRoutines(ctx context.Context, op *session.Operation) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	auth, err := e.authorizationLocked(op)
	if err != nil {
		return err
	}
	if index := auth.Catalog.State().ProcedureNames(); index == nil || len(index.SQLNames) == 0 {
		return nil
	}
	input, _ := sessionPresentation(op.Session)
	for _, descriptor := range auth.Catalog.Descriptors() {
		if err := ctx.Err(); err != nil {
			return err
		}
		if d, ok := descriptor.(*catalog.Procedure); ok && d.SQL != nil {
			auth.State.CurrentUser = d.Owner
			binding, _, bindErr := e.bindSQLRoutineLocked(op, auth, d, input, nil)
			if bindErr != nil && !binding.UnresolvedCallable {
				return bindErr
			}
		}
	}
	return nil
}
