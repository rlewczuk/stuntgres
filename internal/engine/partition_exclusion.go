package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

func (e *Engine) executePartitionExclusionAddLocked(scope *StatementScope, op *session.Operation, auth AuthorizationView, raw *parser.AlterTable, table *catalog.Relation) (Result, error) {
	ref := e.catalog.Domain.Ref(auth.Catalog.Epoch(), table.Address())
	e.mu.Unlock()
	if err := scope.acquirePartitionDescendants(ref, lock.AccessExclusive); err != nil {
		return Result{}, err
	}
	e.mu.Lock()
	auth, err := e.statementAuthorizationLocked(op, raw)
	if err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	if auth.Catalog.Get(table.Address()) != table {
		e.mu.Unlock()
		return Result{}, ids.ErrCatalogReplaced
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.authorizeLocked(auth, op.Session)
	stage.indexes().Output = scope.output
	for _, descriptor := range scope.PartitionRelations {
		stage.domainBases = append(stage.domainBases, descriptor)
	}
	err = partition.StageExclusions(ddlHost{e: e, op: op, stage: stage}, auth, scope.input, table, raw)
	e.mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	if err != nil {
		return Result{}, err
	}
	if _, err = e.ValidateCatalogStage(op.Context(), stage); err != nil {
		return Result{}, err
	}
	return Result{}, e.stageCatalogOverlay(scope, stage, scope.owner.Catalog.Clone())
}
