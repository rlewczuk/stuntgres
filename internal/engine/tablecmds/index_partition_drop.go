package tablecmds

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/parser"
)

// The caller owns Engine.mu; all exits release it before candidate validation.
func executePartitionConstraintDropLocked(e *core.Engine, scope *core.StatementScope, auth core.AuthorizationView, tbl *catalog.Relation, syntax *parser.AlterTable) (core.Result, error) {
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.AuthorizeLocked(auth, scope.Op.Session)
	stage.Indexes().Output = scope.Output
	for _, observed := range scope.PartitionRelations {
		stage.DomainBases = append(stage.DomainBases, observed)
	}
	path, err := core.ReferencePath(auth)
	if err != nil {
		e.Mu.Unlock()
		e.DiscardCatalogStage(stage)
		return core.Result{}, err
	}
	stage.DependencyPath = path
	e.Mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	for _, action := range syntax.Actions {
		captured, err := partition.PrepareConstraintDrop(scope.Op, stage, &stage.Candidate, tbl, action)
		if err != nil {
			return core.Result{}, err
		}
		provider := core.StageDropProvider(scope.Op.Context(), stage, scope, nil, nil)
		dependencies, err := provider.PrepareExpressionDependencies(captured.Roots, action.Mode, ids.ObjectAddress{})
		if err != nil {
			return core.Result{}, err
		}
		if err = captured.Apply(stage); err != nil {
			return core.Result{}, err
		}
		if err = provider.ApplyExpressionDependencies(dependencies); err != nil {
			return core.Result{}, err
		}
	}
	_, err = e.ValidateCatalogStage(scope.Op.Context(), stage)
	if err != nil {
		return core.Result{}, err
	}
	err = e.StageCatalogOverlay(scope, stage, scope.PendingCatalog().Clone())
	return core.Result{}, err
}
