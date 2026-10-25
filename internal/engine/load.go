package engine

import (
	"fmt"

	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

func executeLoad(e *Engine, op *session.Operation, stmt *parser.Load, count int) (Result, error) {
	if count != 0 {
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		return Result{}, err
	}
	return Result{}, ddl.Load(op.Context(), auth.View, stmt)
}
