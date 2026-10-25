package engine

import (
	"fmt"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/parser"
)

// The decision is ddl.AlterAllTablespace; the engine keeps the argument count
// and releases the mutex the dispatcher took.
func alterAllTablespaceLocked(e *Engine, scope *StatementScope, auth AuthorizationView, s *parser.AlterTableAllInTablespace, count int) (Result, error) {
	defer e.Mu.Unlock()
	if count != 0 {
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	return Result{}, ddl.AlterAllTablespace(auth.View, auth, s)
}
