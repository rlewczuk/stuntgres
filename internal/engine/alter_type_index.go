package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

// captureAlterTypeIndexes renders the affected index definitions on the
// statement's deparse path and output.
func captureAlterTypeIndexes(scope *StatementScope, view catalog.Read, original, survivor *catalog.Relation, actions []parser.AlterTableAction) ([]table.AlterTypeIndex, error) {
	return table.CaptureAlterTypeIndexes(scope.Op.Context(), view, scope.Inquiry.DeparsePath, scope.Output, original, survivor, actions)
}

// rebindAlterTypeIndexes rebinds the queued indexes under Engine.mu.
func (e *Engine) rebindAlterTypeIndexes(scope *StatementScope, auth AuthorizationView, parent *catalog.Relation, stage *CatalogStage, queue []table.AlterTypeIndex) error {
	e.Mu.Lock()
	defer e.Mu.Unlock()
	return table.RebindAlterTypeIndexes(alterHost{DDLHost{E: e, Op: scope.Op, CatalogStage: stage}, scope}, auth, parent, queue)
}
