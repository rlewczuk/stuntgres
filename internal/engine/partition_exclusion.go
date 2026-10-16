package engine

import (
	"slices"
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

func (e *Engine) executePartitionExclusionAddLocked(scope *StatementScope, op *Operation, auth AuthorizationView, raw *parser.AlterTable, table *relationDescriptor) (Result, error) {
	ref := ObjectRef{owner: e.owner, Epoch: auth.catalog.Epoch(), Address: table.address}
	e.mu.Unlock()
	if err := scope.acquirePartitionDescendants(ref, accessExclusive); err != nil {
		return Result{}, err
	}
	e.mu.Lock()
	auth, err := e.statementAuthorizationLocked(op, raw)
	if err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	if auth.catalog.state.descriptors[table.address] != table {
		e.mu.Unlock()
		return Result{}, ErrCatalogReplaced
	}
	stage := e.BeginCatalogStage(auth.catalog)
	stage.authorizeLocked(auth, op.session)
	stage.indexOutput = scope.output
	for _, descriptor := range scope.partitionRelations {
		stage.domainBases = append(stage.domainBases, descriptor)
	}
	next := *table
	next.revision, err = e.revisionLocked()
	if err != nil {
		e.mu.Unlock()
		e.DiscardCatalogStage(stage)
		return Result{}, err
	}
	next.exclusionConstraints = slices.Clone(table.exclusionConstraints)
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
