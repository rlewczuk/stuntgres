package engine

import (
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/parser"
)

// Only selected EXCLUDE declarations use the public recursive ADD route.
// Private replay origins for other constraint kinds retain their own fences.
func partitionExclusionAddActions(raw *parser.AlterTable) bool {
	if len(raw.Actions) == 0 {
		return false
	}
	for _, action := range raw.Actions {
		if action.Action != parser.AddExclusion || action.Exclusion == nil {
			return false
		}
	}
	return true
}

func (e *Engine) executePartitionExclusionAddLocked(scope *StatementScope, op *Operation, auth AuthorizationView, raw *parser.AlterTable, table *catalog.Relation) (Result, error) {
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
	stage.authorizeLocked(auth, op.session)
	stage.indexes().output = scope.output
	for _, descriptor := range scope.partitionRelations {
		stage.domainBases = append(stage.domainBases, descriptor)
	}
	next := *table
	next.ObjectRevision, err = e.catalog.revisionLocked()
	if err != nil {
		e.mu.Unlock()
		e.DiscardCatalogStage(stage)
		return Result{}, err
	}
	next.Constraints.Exclusion = slices.Clone(table.Constraints.Exclusion)
	for _, action := range raw.Actions {
		if err = e.stageExclusion(op, scope.input, auth, stage, &next, action.Exclusion, !raw.Only); err != nil {
			break
		}
	}
	e.mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	if err != nil {
		return Result{}, err
	}
	if _, err = e.ValidateCatalogStage(op.Context(), stage); err != nil {
		return Result{}, err
	}
	return Result{}, e.stageCatalogOverlay(scope, stage, cloneCatalogChanges(scope.owner.catalog))
}
