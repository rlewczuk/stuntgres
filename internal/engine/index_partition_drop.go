package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/parser"
)

// stagePartitionConstraintDrop serves ALTER TABLE and the partition
// constraint tests; it does not need Engine.mu.
func (e *Engine) stagePartitionConstraintDrop(stage *CatalogStage, oid ids.OID) error {
	return index.StagePartitionConstraintDrop(stage, oid)
}

// The caller owns Engine.mu; all exits release it before candidate validation.
func (e *Engine) executePartitionConstraintDropLocked(scope *StatementScope, auth AuthorizationView, tbl *catalog.Relation, syntax *parser.AlterTable) (Result, error) {
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.authorizeLocked(auth, scope.op.Session)
	stage.indexes().Output = scope.output
	for _, observed := range scope.PartitionRelations {
		stage.domainBases = append(stage.domainBases, observed)
	}
	e.mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	for _, action := range syntax.Actions {
		roots, err := partition.StageConstraintDrop(scope.op, stage, &stage.Candidate, tbl, action)
		if err != nil {
			return Result{}, err
		}
		if err := scope.stageExpressionDependents(stage, roots, action.Mode, nil); err != nil {
			return Result{}, err
		}
	}
	_, err := e.ValidateCatalogStage(scope.op.Context(), stage)
	if err != nil {
		return Result{}, err
	}
	err = e.stageCatalogOverlay(scope, stage, scope.owner.Catalog.Clone())
	return Result{}, err
}
