package tablecmds

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

// GenerationCandidate binds the replacement under Engine.mu.
func GenerationCandidate(e *core.Engine, scope *core.StatementScope, op *session.Operation, auth core.AuthorizationView, old *catalog.Relation, action parser.AlterTableAction) (*catalog.Relation, int32, error) {
	e.Mu.Lock()
	defer e.Mu.Unlock()
	return table.GenerationCandidate(AlterHost{TableHost{core.DDLHost{E: e, Op: op}}, scope}, auth, old, action)
}
