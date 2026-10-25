package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

// prepareAlterTableActions prepares root actions in their original order; the
// caller holds Engine.mu.
func prepareAlterTableActions(e *core.Engine, scope *core.StatementScope, op *session.Operation, auth core.AuthorizationView, original *catalog.Relation, ref ids.ObjectRef, actions []parser.AlterTableAction) (table.AlterTablePreparation, error) {
	return table.PrepareAlterTableActions(AlterHost{TableHost{core.DDLHost{E: e, Op: op}}, scope}, auth, original, ref, actions)
}
