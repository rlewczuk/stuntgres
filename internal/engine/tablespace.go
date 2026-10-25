package engine

import (
	"fmt"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

// ExecuteCreateTablespace runs CREATE TABLESPACE; ddl.CreateTablespace
// decides under Engine.Mu.
func ExecuteCreateTablespace(e *core.Engine, op *session.Operation, stmt *parser.CreateTablespace, count int) (core.Result, error) {
	if count != 0 {
		return core.Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		return core.Result{}, err
	}
	return core.Result{}, ddl.CreateTablespace(op.Context(), auth.View, stmt)
}
