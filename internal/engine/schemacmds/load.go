package schemacmds

import (
	"fmt"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

// ExecuteLoad authorizes before every activation, including repeated aliases.
func ExecuteLoad(e *core.Engine, op *session.Operation, stmt *parser.Load, count int) (core.Result, error) {
	if count != 0 {
		return core.Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		return core.Result{}, err
	}
	if err := ddl.Load(op.Context(), auth.View, stmt); err != nil {
		return core.Result{}, err
	}
	return core.Result{}, op.EffectiveSettings().LoadOverexplain(op.Context())
}
