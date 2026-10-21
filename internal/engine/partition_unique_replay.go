package engine

import (
	"fmt"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

// This origin belongs to one private-import operation and one captured definition,
// target and subtree, after its intended plain tree is already attached.
func (e *Engine) beginPartitionUniqueReplay(op *session.Operation, raw *parser.AlterTable) error {
	if op == nil {
		return fmt.Errorf("stuntgres: invalid trusted restore partition unique operation")
	}
	operationReplay(op).partitionUnique = nil
	if raw == nil || raw.Only || raw.IfExists || len(raw.Actions) != 1 || raw.Actions[0].Action != parser.AddUnique || raw.Actions[0].Unique == nil {
		return nil
	}
	definition := raw.Actions[0].Unique
	if !strings.HasPrefix(definition.Name.Text, "__stuntgres_partition_key_") {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	auth, err := e.statementAuthorizationLocked(op, raw)
	if err != nil {
		return err
	}
	if !partitionConstraintReplayImport(e, op, auth) {
		return fmt.Errorf("stuntgres: invalid trusted restore partition unique authority")
	}
	ref, err := e.resolveRelationReference(auth, raw.Name, true)
	if err != nil {
		return err
	}
	table, ok := auth.Catalog.Relation(ref.Address)
	if !ok {
		return ids.ErrCatalogReplaced
	}
	captured, err := partition.CaptureUniqueReplay(auth.Catalog, raw, table)
	if err != nil {
		return err
	}
	operationReplay(op).partitionUnique = captured
	return nil
}

func partitionUniqueReplayAllowed(op *session.Operation, auth AuthorizationView, raw *parser.AlterTable, table *catalog.Relation) bool {
	if op == nil || raw == nil || operationReplayOrZero(op).partitionUnique == nil || !partitionConstraintReplayImport(auth.engine, op, auth) {
		return false
	}
	return operationReplayOrZero(op).partitionUnique.Matches(auth.Catalog, raw, table)
}

// Consume Engine.mu like the canonical ALTER executor. The existing binder,
// recursive virtual-index producer, validator and Finish overlay own all effects.
func (e *Engine) executePartitionUniqueReplayLocked(scope *StatementScope, op *session.Operation, auth AuthorizationView, raw *parser.AlterTable, table *catalog.Relation) (Result, error) {
	ref := e.catalog.Domain.Ref(auth.Catalog.Epoch(), table.Address())
	origin := operationReplayOrZero(op).partitionUnique
	e.mu.Unlock()
	if err := scope.acquirePartitionDescendants(ref, lock.AccessExclusive); err != nil {
		return Result{}, err
	}
	e.mu.Lock()
	auth, err := e.statementAuthorizationLocked(op, raw)
	if err == nil && !partitionUniqueReplayAllowed(op, auth, raw, table) {
		err = ids.ErrCatalogReplaced
	}
	if err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.authorizeLocked(auth, op.Session)
	stage.indexes().Output = scope.output
	for _, descriptor := range origin.Descriptors {
		stage.domainBases = append(stage.domainBases, descriptor)
	}
	e.mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	next, err := e.stageAddedUnique(op, auth, stage, table, origin.Definition)
	if err != nil {
		return Result{}, err
	}
	idx := e.stageRead(stage).MustRelation(next.Constraints.Unique[len(next.Constraints.Unique)-1].Index)
	if err = index.PartitionUniqueCoverage(next, idx.SQLIndex); err != nil {
		return Result{}, err
	}
	e.mu.Lock()
	err = e.stagePartitionIndexChildren(op, stage, next, idx, true)
	e.mu.Unlock()
	if err != nil {
		return Result{}, err
	}
	if next.Revision() == ids.DefinitionRevision(^uint64(0)) {
		return Result{}, fmt.Errorf("stuntgres: definition revision exhausted")
	}
	next.ObjectRevision++
	if err = stage.Replace(next); err != nil {
		return Result{}, err
	}
	if _, err = e.ValidateCatalogStage(op.Context(), stage); err != nil {
		return Result{}, err
	}
	return Result{}, e.stageCatalogOverlay(scope, stage, scope.owner.Catalog.Clone())
}
