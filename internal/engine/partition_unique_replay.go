package engine

import (
	"fmt"
	"slices"
	"strings"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

type partitionUniqueReplay struct {
	target      *relationDescriptor
	syntax      *parser.AlterTable
	definition  *parser.UniqueConstraint
	snapshot    parser.UniqueConstraint
	descriptors []*relationDescriptor
}

// This origin belongs to one private-import operation and one captured definition,
// target and subtree, after its intended plain tree is already attached.
func (e *Engine) beginPartitionUniqueReplay(op *Operation, raw *parser.AlterTable) error {
	if op == nil {
		return fmt.Errorf("stuntgres: invalid trusted restore partition unique operation")
	}
	op.restorePartitionUnique = nil
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
	table, ok := auth.catalog.Relation(ref.Address)
	if !ok {
		return ErrCatalogReplaced
	}
	if table.partition == nil || table.membership == nil || table.inheritance != nil {
		return nil
	}
	bound, err := bindUniqueAttributes(definition, nil, table.index, table.attributes)
	if err != nil {
		return err
	}
	parent := auth.catalog.MustRelation(table.membership.parent)
	chosen := partitionPreservedReplayChild(auth.catalog, parent)
	if chosen == nil || chosen.address != table.address {
		return nil
	}
	var selected *relationDescriptor
	for _, descriptor := range auth.catalog.Descriptors() {
		index, ok := descriptor.(*relationDescriptor)
		if !ok || index.sqlIndex == nil || index.sqlIndex.parent != parent.address || !partitionIndexPreservedInvalid(auth.catalog, index) {
			continue
		}
		child := partitionDumpIndexBacking(auth.catalog, index.address, table.address)
		if child == nil || child.sqlIndex.constraint != nil || child.sqlIndex.predicate != nil || len(child.sqlIndex.include) != 0 || child.sqlIndex.nullsNotDistinct != bound.NullsNotDistinct || len(child.sqlIndex.keys) != len(bound.Attributes) {
			continue
		}
		match := true
		for i, key := range child.sqlIndex.keys {
			match = match && key.derived == nil && key.attribute == bound.Attributes[i]
		}
		if match {
			selected = index
			break
		}
	}
	if selected == nil {
		return nil
	}
	captured := &partitionUniqueReplay{target: table, syntax: raw, definition: definition, snapshot: *definition, descriptors: []*relationDescriptor{parent, selected}}
	captured.snapshot.Columns = slices.Clone(definition.Columns)
	tables := make(map[ids.ObjectAddress]bool)
	var capture func(*relationDescriptor) bool
	capture = func(node *relationDescriptor) bool {
		if node.inheritance != nil {
			return false
		}
		if definition.Primary {
			for _, column := range definition.Columns {
				slot, ok := node.index[column.Text]
				if !ok || node.columns[slot].Nullable {
					return false
				}
			}
		}
		tables[node.address] = true
		captured.descriptors = append(captured.descriptors, node)
		if node.partition != nil {
			for _, address := range node.partition.children {
				if !capture(auth.catalog.MustRelation(address)) {
					return false
				}
			}
		}
		return true
	}
	if !capture(table) {
		return nil
	}
	for _, descriptor := range auth.catalog.Descriptors() {
		index, ok := descriptor.(*relationDescriptor)
		if ok && index.sqlIndex != nil && tables[index.sqlIndex.parent] {
			captured.descriptors = append(captured.descriptors, index)
		}
	}
	op.restorePartitionUnique = captured
	return nil
}

func partitionUniqueReplayAllowed(op *Operation, auth AuthorizationView, raw *parser.AlterTable, table *relationDescriptor) bool {
	if op == nil || raw == nil || op.restorePartitionUnique == nil || !partitionConstraintReplayImport(auth.engine, op, auth) {
		return false
	}
	origin := op.restorePartitionUnique
	if origin.target != table || origin.syntax != raw || raw.Only || raw.IfExists || len(raw.Actions) != 1 || raw.Actions[0].Action != parser.AddUnique || raw.Actions[0].Unique != origin.definition {
		return false
	}
	current := origin.definition
	saved := origin.snapshot
	if current.Name != saved.Name || current.Primary != saved.Primary || current.NullsNotDistinct != saved.NullsNotDistinct || current.Pos != saved.Pos || !slices.Equal(current.Columns, saved.Columns) {
		return false
	}
	for _, descriptor := range origin.descriptors {
		if auth.catalog.Get(descriptor.address) != descriptor {
			return false
		}
	}
	return true
}

// Consume Engine.mu like the canonical ALTER executor. The existing binder,
// recursive virtual-index producer, validator and Finish overlay own all effects.
func (e *Engine) executePartitionUniqueReplayLocked(scope *StatementScope, op *Operation, auth AuthorizationView, raw *parser.AlterTable, table *relationDescriptor) (Result, error) {
	ref := e.catalog.domain.Ref(auth.catalog.Epoch(), table.address)
	origin := op.restorePartitionUnique
	e.mu.Unlock()
	if err := scope.acquirePartitionDescendants(ref, accessExclusive); err != nil {
		return Result{}, err
	}
	e.mu.Lock()
	auth, err := e.statementAuthorizationLocked(op, raw)
	if err == nil && !partitionUniqueReplayAllowed(op, auth, raw, table) {
		err = ErrCatalogReplaced
	}
	if err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	stage := e.BeginCatalogStage(auth.catalog)
	stage.authorizeLocked(auth, op.session)
	stage.indexOutput = scope.output
	for _, descriptor := range origin.descriptors {
		stage.domainBases = append(stage.domainBases, descriptor)
	}
	e.mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	next, err := e.stageAddedUnique(op, auth, stage, table, origin.definition)
	if err != nil {
		return Result{}, err
	}
	index := e.stageRead(stage).MustRelation(next.uniqueConstraints[len(next.uniqueConstraints)-1].Index)
	if err = partitionUniqueCoverage(next, index.sqlIndex); err != nil {
		return Result{}, err
	}
	e.mu.Lock()
	err = e.stagePartitionIndexChildren(op, stage, next, index, true)
	e.mu.Unlock()
	if err != nil {
		return Result{}, err
	}
	if next.revision == ids.DefinitionRevision(^uint64(0)) {
		return Result{}, fmt.Errorf("stuntgres: definition revision exhausted")
	}
	next.revision++
	if err = stage.Replace(next); err != nil {
		return Result{}, err
	}
	if _, err = e.ValidateCatalogStage(op.Context(), stage); err != nil {
		return Result{}, err
	}
	return Result{}, e.stageCatalogOverlay(scope, stage, cloneCatalogChanges(scope.owner.catalog))
}
