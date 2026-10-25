package tablecmds

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/parser"
)

// stagePartitionConstraintDrop serves ALTER TABLE and the partition
// constraint tests; it does not need Engine.mu.
func stagePartitionConstraintDrop(e *core.Engine, stage *core.CatalogStage, oid ids.OID) error {
	return index.StagePartitionConstraintDrop(stage, oid)
}

// The caller owns Engine.mu; all exits release it before candidate validation.
func executePartitionConstraintDropLocked(e *core.Engine, scope *core.StatementScope, auth core.AuthorizationView, tbl *catalog.Relation, syntax *parser.AlterTable) (core.Result, error) {
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
			return core.Result{}, err
		}
		if err := scope.StageExpressionDependents(stage, roots, action.Mode, nil); err != nil {
			return core.Result{}, err
		}
	}
	_, err := e.ValidateCatalogStage(scope.Op.Context(), stage)
	if err != nil {
		return core.Result{}, err
	}
	err = e.StageCatalogOverlay(scope, stage, scope.Owner.Catalog.Clone())
	return core.Result{}, err
}
