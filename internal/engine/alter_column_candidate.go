package engine

import (
	"context"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

// Stable attributes own defaults, ACLs, comments, constraints and dependency edges.
func (scope *StatementScope) dropColumnCandidate(stage *CatalogStage, old *catalog.Relation, ref ids.ObjectRef, a parser.AlterTableAction, changes *session.Journal) (*catalog.Relation, error) {
	return dropColumnCandidate(scope.op.Context(), stage, old, ref, a, scope, changes, nil)
}

func dropColumnCandidate(ctx context.Context, stage *CatalogStage, old *catalog.Relation, ref ids.ObjectRef, a parser.AlterTableAction, scope *StatementScope, changes *session.Journal, project func(*catalog.Relation, *catalog.Relation) error) (*catalog.Relation, error) {
	return ordinaryColumnDrop(stageDropProvider(ctx, stage, scope, changes, project), old, ref, a)
}
