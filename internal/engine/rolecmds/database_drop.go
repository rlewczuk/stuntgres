package rolecmds

import (
	"fmt"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/parser"
)

func ExecuteDropDatabase(e *core.Engine, scope *core.StatementScope, statement *parser.DropDatabaseStmt, count int) (core.Result, error) {
	if count != 0 {
		return core.Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	return e.DropDatabase(scope, statement)
}
