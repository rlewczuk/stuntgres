package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// The ADD COLUMN candidate runs under Engine.mu; DEFAULT recipes keep the
// statement's authority for row fills.
func (e *Engine) addColumnCandidate(scope *StatementScope, op *session.Operation, auth AuthorizationView, old *catalog.Relation, action parser.AlterTableAction, captured *sqltype.Usage) (*catalog.Relation, table.AddedColumn, error) {
	scope.RoutineAuth = auth
	e.Mu.Lock()
	defer e.Mu.Unlock()
	return table.AddColumnCandidate(alterHost{TableHost{DDLHost{E: e, Op: op}}, scope}, auth, old, action, captured)
}
