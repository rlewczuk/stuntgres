package tablecmds

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

// CaptureAlterTypeIndexes renders the affected index definitions on the
// statement's deparse path and output.
func CaptureAlterTypeIndexes(scope *core.StatementScope, view catalog.Read, original, survivor *catalog.Relation, actions []parser.AlterTableAction) ([]table.AlterTypeIndex, error) {
	return table.CaptureAlterTypeIndexes(scope.Op.Context(), view, scope.Inquiry.DeparsePath, scope.Output, original, survivor, actions)
}

// RebindAlterTypeIndexes rebinds the queued indexes under Engine.mu.
func RebindAlterTypeIndexes(e *core.Engine, scope *core.StatementScope, auth core.AuthorizationView, parent *catalog.Relation, stage *core.CatalogStage, queue []table.AlterTypeIndex) error {
	e.Mu.Lock()
	defer e.Mu.Unlock()
	return table.RebindAlterTypeIndexes(AlterHost{TableHost{core.DDLHost{E: e, Op: scope.Op, CatalogStage: stage}}, scope}, auth, parent, queue)
}
