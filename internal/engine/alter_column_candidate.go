package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

// Stable attributes own defaults, ACLs, comments, constraints and dependency edges.
func (scope *StatementScope) dropColumnCandidate(stage *CatalogStage, old *catalog.Relation, ref ids.ObjectRef, a parser.AlterTableAction, changes *session.Journal) (*catalog.Relation, error) {
	return table.OrdinaryColumnDrop(StageDropProvider(scope.Op.Context(), stage, scope, changes, nil), old, ref, a)
}
