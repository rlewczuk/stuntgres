package engine

import (
	"context"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/session"
)

// validateRestoredSQLRoutines binds every restored SQL routine body as its
// owner once Restore has replayed the whole script.

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
			if _, _, err = e.bindSQLRoutineLocked(op, auth, d, input, nil); err != nil {
				return err
			}
		}
	}
	return nil
}
