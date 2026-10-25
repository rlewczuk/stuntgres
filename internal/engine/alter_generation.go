package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

// generationCandidate binds the replacement under Engine.mu.
func (e *Engine) generationCandidate(scope *StatementScope, op *session.Operation, auth AuthorizationView, old *catalog.Relation, action parser.AlterTableAction) (*catalog.Relation, int32, error) {
	e.Mu.Lock()
	defer e.Mu.Unlock()
	return table.GenerationCandidate(alterHost{TableHost{DDLHost{E: e, Op: op}}, scope}, auth, old, action)
}
