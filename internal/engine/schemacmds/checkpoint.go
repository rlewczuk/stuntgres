package schemacmds

import (
	"fmt"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

// ExecuteCheckpoint captures fresh authority under Engine.Mu; it never completes
// a utility or publishes a successful physical operation.
func ExecuteCheckpoint(e *core.Engine, op *session.Operation, stmt *parser.Checkpoint, count int) (core.Result, error) {
	if count != 0 {
		return core.Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		return core.Result{}, err
	}
	return core.Result{}, ddl.Checkpoint(op.Context(), auth.View, stmt)
}
