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
	module, err := ddl.Load(op.Context(), auth.View, stmt)
	if err != nil {
		return core.Result{}, err
	}
	switch module {
	case ddl.LoadOverexplain:
		return core.Result{}, op.EffectiveSettings().LoadOverexplain(op.Context())
	case ddl.LoadSepgsql:
		// Registration already owns provider activation. Keep physical terminal
		// admission without activating another module or changing settings.
		physical := op.Session.Settings
		ctx := op.Context()
		physical.Mu.Lock()
		defer physical.Mu.Unlock()
		if err := ctx.Err(); err != nil {
			return core.Result{}, err
		}
		if physical.Ended {
			return core.Result{}, fmt.Errorf("stuntgres: session has ended")
		}
		return core.Result{}, nil
	default:
		return core.Result{}, fmt.Errorf("stuntgres: invalid LOAD module %d", module)
	}
}
