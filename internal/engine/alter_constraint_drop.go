package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

// Retire named constraint identities and original backing generations after
// logical DROP, before ADD naming.
// Remove retains the original reference/name observations and locks the engine;
// neither retirement nor dependency traversal publishes the private candidate.
// Each action classifies its own dependencies before any later backing removal.
func stageDroppedConstraintIndexes(e *core.Engine, scope *core.StatementScope, auth core.AuthorizationView, stage *core.CatalogStage, old, next *catalog.Relation, actions []parser.AlterTableAction, changes *session.Journal) (*catalog.Relation, *core.CatalogStage, error) {
	if !table.HasDroppedConstraintIndexes(old, actions) {
		return next, stage, nil
	}
	if stage == nil {
		stage = e.BeginCatalogStage(auth.Catalog)
		stage.AuthSession, stage.AuthRevision, stage.RoleRevision = scope.Op.Session, auth.Revision, auth.RoleRecord.Revision
	}
	next, err := table.DropConstraintIndexes(core.StageDropProvider(scope.Op.Context(), stage, scope, changes, nil), old, next, actions)
	return next, stage, err
}

func prepareConstraintIndexDrop(scope *core.StatementScope, auth core.AuthorizationView, old, next *catalog.Relation, actions []parser.AlterTableAction, prior *table.DropPreparation) (*table.DropPreparation, error) {
	if !table.HasDroppedConstraintIndexes(old, actions) {
		return nil, nil
	}
	provider, err := prepareDropDefinitionProvider(scope, auth, next, scope.LockedRef, prior)
	if err != nil {
		return nil, err
	}
	next, err = table.DropConstraintIndexes(provider, old, next, actions)
	if err != nil {
		return nil, err
	}
	return finishDropDefinition(scope, provider, next)
}
