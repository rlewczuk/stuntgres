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
	scope.routineAuth = auth
	e.mu.Lock()
	defer e.mu.Unlock()
	return table.AddColumnCandidate(alterHost{ddlHost{e: e, op: op}, scope}, auth, old, action, captured)
}
