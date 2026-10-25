package schemacmds

import (
	"fmt"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/parser"
)

// AlterAllTablespaceLocked runs ALTER TABLE ALL IN TABLESPACE, entered with
// Engine.Mu held.
//
// The decision is ddl.AlterAllTablespace; the engine keeps the argument count
// and releases the mutex the dispatcher took.
func AlterAllTablespaceLocked(e *core.Engine, scope *core.StatementScope, auth core.AuthorizationView, s *parser.AlterTableAllInTablespace, count int) (core.Result, error) {
	defer e.Mu.Unlock()
	if count != 0 {
		return core.Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	return core.Result{}, ddl.AlterAllTablespace(auth.View, auth, s)
}
