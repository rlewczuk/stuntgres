package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// prepareAlterTypeLocked admits and completes one TYPE action; the caller holds
// Engine.mu. suppliedUsage is used only by typed propagation of an already
// resolved type.
func (e *Engine) prepareAlterTypeLocked(scope *StatementScope, op *session.Operation, auth AuthorizationView, original *catalog.Relation, ref ids.ObjectRef, a parser.AlterTableAction, suppliedUsage *sqltype.Usage) (table.AlterTypePreparation, error) {
	return table.PrepareAlterType(alterHost{TableHost{DDLHost{E: e, Op: op}}, scope}, auth, original, ref, a, suppliedUsage)
}
