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
	stage.AuthorizeLocked(auth, scope.Op.Session)
	stage.Indexes().Output = scope.Output
	for _, observed := range scope.PartitionRelations {
		stage.DomainBases = append(stage.DomainBases, observed)
	}
	e.Mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	for _, action := range syntax.Actions {
		roots, err := partition.StageConstraintDrop(scope.Op, stage, &stage.Candidate, tbl, action)
		if err != nil {
			return Result{}, err
		}
		if err := scope.StageExpressionDependents(stage, roots, action.Mode, nil); err != nil {
			return Result{}, err
		}
	}
	_, err := e.ValidateCatalogStage(scope.Op.Context(), stage)
	if err != nil {
		return Result{}, err
	}
	err = e.StageCatalogOverlay(scope, stage, scope.Owner.Catalog.Clone())
	return Result{}, err
}
