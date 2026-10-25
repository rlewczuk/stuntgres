package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

// prepareAlterTableActions prepares root actions in their original order; the
// caller holds Engine.mu.
func prepareAlterTableActions(e *Engine, scope *StatementScope, op *session.Operation, auth AuthorizationView, original *catalog.Relation, ref ids.ObjectRef, actions []parser.AlterTableAction) (table.AlterTablePreparation, error) {
	return table.PrepareAlterTableActions(alterHost{TableHost{DDLHost{E: e, Op: op}}, scope}, auth, original, ref, actions)
}
