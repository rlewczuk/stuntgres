package engine

import (
	"fmt"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

func (e *Engine) executeCreateTablespace(op *session.Operation, stmt *parser.CreateTablespace, count int) (Result, error) {
	if count != 0 {
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	auth, err := e.authorizationLocked(op)
	if err != nil {
		return Result{}, err
	}
	return Result{}, createTablespace(op.Context(), auth.View, stmt)
}
