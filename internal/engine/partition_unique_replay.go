package engine

import (
	"fmt"
	"slices"
	"strings"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/parser"
)

type partitionUniqueReplay struct {
	target      *catalog.Relation
	syntax      *parser.AlterTable
	definition  *parser.UniqueConstraint
	snapshot    parser.UniqueConstraint
	descriptors []*catalog.Relation
}

// This origin belongs to one private-import operation and one captured definition,
// target and subtree, after its intended plain tree is already attached.
func (e *Engine) beginPartitionUniqueReplay(op *Operation, raw *parser.AlterTable) error {
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
	if table.Partition == nil || table.Membership == nil || table.Inheritance != nil {
		return nil
	}
	bound, err := analyze.BindUniqueAttributes(definition, nil, table.Index, table.Attributes)
	if err != nil {
		return err
	}
	parent := auth.Catalog.MustRelation(table.Membership.Parent)
	chosen := partitionPreservedReplayChild(auth.Catalog, parent)
	if chosen == nil || chosen.Address() != table.Address() {
		return nil
	}
	var selected *catalog.Relation
	for _, descriptor := range auth.Catalog.Descriptors() {
		index, ok := descriptor.(*catalog.Relation)
		if !ok || index.SQLIndex == nil || index.SQLIndex.Parent != parent.Address() || !partitionIndexPreservedInvalid(auth.Catalog, index) {
			continue
		}
		child := partitionDumpIndexBacking(auth.Catalog, index.Address(), table.Address())
		if child == nil || child.SQLIndex.Constraint != nil || child.SQLIndex.Predicate != nil || len(child.SQLIndex.Include) != 0 || child.SQLIndex.NullsNotDistinct != bound.NullsNotDistinct || len(child.SQLIndex.Keys) != len(bound.Attributes) {
			continue
		}
		match := true
		for i, key := range child.SQLIndex.Keys {
			match = match && key.Derived == nil && key.Attribute == bound.Attributes[i]
		}
		if match {
			selected = index
			break
		}
	}
	if selected == nil {
		return nil
	}
	captured := &partitionUniqueReplay{target: table, syntax: raw, definition: definition, snapshot: *definition, descriptors: []*catalog.Relation{parent, selected}}
	captured.snapshot.Columns = slices.Clone(definition.Columns)
	tables := make(map[ids.ObjectAddress]bool)
	var capture func(*catalog.Relation) bool
	capture = func(node *catalog.Relation) bool {
		if node.Inheritance != nil {
			return false
		}
		if definition.Primary {
			for _, column := range definition.Columns {
				slot, ok := node.Index[column.Text]
				if !ok || node.Columns[slot].Nullable {
					return false
				}
			}
		}
		tables[node.Address()] = true
		captured.descriptors = append(captured.descriptors, node)
		if node.Partition != nil {
			for _, address := range node.Partition.Children {
				if !capture(auth.Catalog.MustRelation(address)) {
					return false
				}
			}
		}
		return true
	}
	if !capture(table) {
		return nil
	}
	for _, descriptor := range auth.Catalog.Descriptors() {
		index, ok := descriptor.(*catalog.Relation)
		if ok && index.SQLIndex != nil && tables[index.SQLIndex.Parent] {
			captured.descriptors = append(captured.descriptors, index)
		}
	}
	operationReplay(op).partitionUnique = captured
	return nil
}

func partitionUniqueReplayAllowed(op *Operation, auth AuthorizationView, raw *parser.AlterTable, table *catalog.Relation) bool {
	if op == nil || raw == nil || operationReplayOrZero(op).partitionUnique == nil || !partitionConstraintReplayImport(auth.engine, op, auth) {
		return false
	}
	origin := operationReplayOrZero(op).partitionUnique
	if origin.target != table || origin.syntax != raw || raw.Only || raw.IfExists || len(raw.Actions) != 1 || raw.Actions[0].Action != parser.AddUnique || raw.Actions[0].Unique != origin.definition {
		return false
	}
	current := origin.definition
	saved := origin.snapshot
	if current.Name != saved.Name || current.Primary != saved.Primary || current.NullsNotDistinct != saved.NullsNotDistinct || current.Pos != saved.Pos || !slices.Equal(current.Columns, saved.Columns) {
		return false
	}
	for _, descriptor := range origin.descriptors {
		if auth.Catalog.Get(descriptor.Address()) != descriptor {
			return false
		}
	}
	return true
}

// Consume Engine.mu like the canonical ALTER executor. The existing binder,
// recursive virtual-index producer, validator and Finish overlay own all effects.
func (e *Engine) executePartitionUniqueReplayLocked(scope *StatementScope, op *Operation, auth AuthorizationView, raw *parser.AlterTable, table *catalog.Relation) (Result, error) {
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
	stage.indexes().output = scope.output
	for _, descriptor := range origin.descriptors {
		stage.domainBases = append(stage.domainBases, descriptor)
	}
	e.mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	next, err := e.stageAddedUnique(op, auth, stage, table, origin.definition)
	if err != nil {
		return Result{}, err
	}
	index := e.stageRead(stage).MustRelation(next.Constraints.Unique[len(next.Constraints.Unique)-1].Index)
	if err = partitionUniqueCoverage(next, index.SQLIndex); err != nil {
		return Result{}, err
	}
	e.mu.Lock()
	err = e.stagePartitionIndexChildren(op, stage, next, index, true)
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
