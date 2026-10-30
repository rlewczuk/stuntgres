package engine

import (
	"context"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/schemacmds"
	"stuntgres/internal/engine/session"
)

// validateRestoredSQLRoutines validates bodies as their owners after replay.
// The private SQL-body window reconstructs quoted declarations without binding
// their soft source. Genuine invocation still binds and checks reached results.
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
	input, _ := op.EffectiveSettings().Presentation()
	for _, descriptor := range auth.Catalog.Descriptors() {
		if err := ctx.Err(); err != nil {
			return err
		}
		if d, ok := descriptor.(*catalog.Procedure); ok && d.SQL != nil {
			auth.State.CurrentUser = d.Owner
			if d.SQL.Kind == proc.PLpgSQLBody {
				// A source-only declaration deliberately has no CREATE validator
				// result. Replay must preserve that state, including malformed
				// source; only reached invocation may compile it.
				if d.SQL.PL == nil {
					continue
				}
				if err := e.CompilePLRoutineLocked(op, auth, d); err != nil {
					return err
				}
				continue
			}
			if core.OperationReplayOrZero(op).SQLBodies && !d.SQL.Standard {
				continue
			}
			_, _, bindErr := schemacmds.BindSQLRoutineLocked(e, op, auth, d, input, true)
			if bindErr != nil {
				return bindErr
			}
		}
	}
	return nil
}
