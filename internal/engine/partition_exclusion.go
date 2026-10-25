package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

func executePartitionExclusionAddLocked(e *core.Engine, scope *core.StatementScope, op *session.Operation, auth core.AuthorizationView, raw *parser.AlterTable, table *catalog.Relation) (core.Result, error) {
	ref := e.Catalog.Domain.Ref(auth.Catalog.Epoch(), table.Address())
	e.Mu.Unlock()
	if err := scope.AcquirePartitionDescendants(ref, lock.AccessExclusive); err != nil {
		return core.Result{}, err
	}
	e.Mu.Lock()
	auth, err := e.StatementAuthorizationLocked(op, raw)
	if err != nil {
		e.Mu.Unlock()
		return core.Result{}, err
	}
	if auth.Catalog.Get(table.Address()) != table {
		e.Mu.Unlock()
		return core.Result{}, ids.ErrCatalogReplaced
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.AuthorizeLocked(auth, op.Session)
	stage.Indexes().Output = scope.Output
	for _, descriptor := range scope.PartitionRelations {
		stage.DomainBases = append(stage.DomainBases, descriptor)
	}
	err = partition.StageExclusions(TableHost{core.DDLHost{E: e, Op: op, CatalogStage: stage}}, auth, scope.Input, table, raw)
	e.Mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	if err != nil {
		return core.Result{}, err
	}
	if _, err = e.ValidateCatalogStage(op.Context(), stage); err != nil {
		return core.Result{}, err
	}
	return core.Result{}, e.StageCatalogOverlay(scope, stage, scope.Owner.Catalog.Clone())
}
