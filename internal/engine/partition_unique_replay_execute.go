package engine

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

// Consume Engine.mu like the canonical ALTER executor. The existing binder,
// recursive virtual-index producer, validator and Finish overlay own all effects.
func executePartitionUniqueReplayLocked(e *core.Engine, scope *core.StatementScope, op *session.Operation, auth core.AuthorizationView, raw *parser.AlterTable, table *catalog.Relation) (core.Result, error) {
	ref := e.Catalog.Domain.Ref(auth.Catalog.Epoch(), table.Address())
	origin := core.OperationReplayOrZero(op).PartitionUnique
	e.Mu.Unlock()
	if err := scope.AcquirePartitionDescendants(ref, lock.AccessExclusive); err != nil {
		return core.Result{}, err
	}
	e.Mu.Lock()
	auth, err := e.StatementAuthorizationLocked(op, raw)
	if err == nil && !core.PartitionUniqueReplayAllowed(op, auth, raw, table) {
		err = ids.ErrCatalogReplaced
	}
	if err != nil {
		e.Mu.Unlock()
		return core.Result{}, err
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.AuthorizeLocked(auth, op.Session)
	stage.Indexes().Output = scope.Output
	for _, descriptor := range origin.Descriptors {
		stage.DomainBases = append(stage.DomainBases, descriptor)
	}
	e.Mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	next, err := stageAddedUnique(e, op, auth, stage, table, origin.Definition)
	if err != nil {
		return core.Result{}, err
	}
	idx := e.StageRead(stage).MustRelation(next.Constraints.Unique[len(next.Constraints.Unique)-1].Index)
	if err = index.PartitionUniqueCoverage(e.StageRead(stage), next, idx.SQLIndex); err != nil {
		return core.Result{}, index.DefaultClassDiagnostic(core.DDLHost{E: e, Op: op, CatalogStage: stage}, auth, err)
	}
	e.Mu.Lock()
	err = stagePartitionIndexChildren(e, op, stage, next, idx, true)
	e.Mu.Unlock()
	if err != nil {
		return core.Result{}, index.DefaultClassDiagnostic(core.DDLHost{E: e, Op: op, CatalogStage: stage}, auth, err)
	}
	if next.Revision() == ids.DefinitionRevision(^uint64(0)) {
		return core.Result{}, fmt.Errorf("stuntgres: definition revision exhausted")
	}
	next.ObjectRevision++
	if err = stage.Replace(next); err != nil {
		return core.Result{}, err
	}
	if _, err = e.ValidateCatalogStage(op.Context(), stage); err != nil {
		return core.Result{}, err
	}
	return core.Result{}, e.StageCatalogOverlay(scope, stage, scope.Owner.Catalog.Clone())
}
