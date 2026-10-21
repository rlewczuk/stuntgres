package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

// generationCandidate binds the replacement under Engine.mu.
func (e *Engine) generationCandidate(scope *StatementScope, op *session.Operation, auth AuthorizationView, old *catalog.Relation, action parser.AlterTableAction) (*catalog.Relation, int32, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return table.GenerationCandidate(alterHost{ddlHost{e: e, op: op}, scope}, auth, old, action)
}
