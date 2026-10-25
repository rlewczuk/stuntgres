package engine

import (
	"context"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

// StageOwnedExpressionDependencies calls stageOwnedExpressionDependencies.
func StageOwnedExpressionDependencies(ctx context.Context, stage *CatalogStage, targets []ids.ObjectAddress, mode parser.DropMode, scope *StatementScope, changes *session.Journal, project func(*catalog.Relation, *catalog.Relation) error) error {
	return stageOwnedExpressionDependencies(ctx, stage, targets, mode, scope, changes, project)
}

// EngineSequenceEnvironmentLocked calls x.sequenceEnvironmentLocked.
func EngineSequenceEnvironmentLocked(x *Engine, auth AuthorizationView) (sequence.Environment, error) {
	return x.sequenceEnvironmentLocked(auth)
}

// EngineSequenceAnalysisValidLocked calls x.sequenceAnalysisValidLocked.
func EngineSequenceAnalysisValidLocked(x *Engine, auth AuthorizationView, analysis *sequence.Analysis) (bool, error) {
	return x.sequenceAnalysisValidLocked(auth, analysis)
}
