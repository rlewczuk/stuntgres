package tablecmds

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// The ADD COLUMN candidate runs under Engine.mu; DEFAULT recipes keep the
// statement's authority for row fills.
func AddColumnCandidate(e *core.Engine, scope *core.StatementScope, op *session.Operation, auth core.AuthorizationView, old *catalog.Relation, action parser.AlterTableAction, captured *sqltype.Usage) (*catalog.Relation, table.AddedColumn, error) {
	scope.RoutineAuth = auth
	e.Mu.Lock()
	defer e.Mu.Unlock()
	return table.AddColumnCandidate(AlterHost{TableHost{core.DDLHost{E: e, Op: op}}, scope}, auth, old, action, captured)
}
