package engine

import (
	"fmt"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

func executeCreateTablespace(e *Engine, op *session.Operation, stmt *parser.CreateTablespace, count int) (Result, error) {
	if count != 0 {
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		return Result{}, err
	}
	return Result{}, ddl.CreateTablespace(op.Context(), auth.View, stmt)
}
