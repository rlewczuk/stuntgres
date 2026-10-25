package core

import (
	"context"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

// AUTO dependencies use stable attribute SubIDs, never compacted column slots.
func (s *StatementScope) StageOwnedSequences(stage *CatalogStage, targets []ids.ObjectAddress, mode parser.DropMode, changes *session.Journal) error {
	return stageOwnedExpressionDependencies(s.Op.Context(), stage, targets, mode, s, changes, nil)
}

func stageOwnedExpressionDependencies(ctx context.Context, stage *CatalogStage, targets []ids.ObjectAddress, mode parser.DropMode, scope *StatementScope, changes *session.Journal, project func(*catalog.Relation, *catalog.Relation) error) error {
	return table.OwnedExpressionDependencies(StageDropProvider(ctx, stage, scope, changes, project), targets, mode)
}
