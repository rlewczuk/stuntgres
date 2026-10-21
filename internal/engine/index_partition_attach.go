package engine

import (
	"context"
	"fmt"
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

func (e *Engine) stagePartitionIndexValidity(stage *CatalogStage, promote ids.ObjectAddress) error {
	return stageDropProvider(context.Background(), stage, nil, nil, nil).partitionIndexValidity(promote)
}

func (provider *dropProvider) partitionIndexValidity(promote ids.ObjectAddress) error {
	// Only explicit ATTACH INDEX can promote that index and its ancestors.
	// Table edge changes preserve invalidity even when coverage becomes complete.
	// Each pass resolves another level; the catalog validator rejects cycles.
	for range provider.descriptorCount() {
		changed := false
		for address := range provider.Descriptors() {
			if provider.Removed(address) {
				continue
			}
			descriptor := provider.LiveDescriptor(address)
			index, ok := descriptor.(*catalog.Relation)
			if !ok || index.SQLIndex == nil {
				continue
			}
			table, ok := provider.LiveDescriptor(index.SQLIndex.Parent).(*catalog.Relation)
			if !ok || table.Partition == nil {
				continue
			}
			valid := CoverageComplete(provider, index)
			if valid && !index.SQLIndex.Valid && !provider.partitionIndexPromotionPath(promote, index.Address()) {
				continue
			}
			if index.SQLIndex.Valid == valid {
				continue
			}
			if index.Revision() == ids.DefinitionRevision(^uint64(0)) {
				return fmt.Errorf("stuntgres: definition revision exhausted")
			}
			next, definition := *index, *index.SQLIndex
			definition.Valid = valid
			next.SQLIndex = &definition
			next.ObjectRevision++
			if err := provider.replacePartitionDescriptor(&next); err != nil {
				return err
			}
			changed = true
		}
		if !changed {
			return nil
		}
	}
	return fmt.Errorf("stuntgres: cyclic partition index hierarchy")
}

func partitionIndexPromotionPath(view catalog.Read, start, target ids.ObjectAddress) bool {
	return (&dropProvider{base: view}).partitionIndexPromotionPath(start, target)
}

func (provider *dropProvider) partitionIndexPromotionPath(start, target ids.ObjectAddress) bool {
	for remaining := provider.descriptorCount(); start != (ids.ObjectAddress{}) && remaining > 0; remaining-- {
		if start == target {
			return true
		}
		index, ok := provider.LiveDescriptor(start).(*catalog.Relation)
		if !ok || index.SQLIndex == nil {
			return false
		}
		start = index.SQLIndex.PartitionParent
	}
	return false
}

func (e *Engine) stagePartitionIndexAttach(op *session.Operation, stage *CatalogStage, parent, child *catalog.Relation) error {
	view := e.stageRead(stage)
	if err := PartitionIndexAttachCheck(op, view, parent, child); err != nil {
		return err
	}
	if child.SQLIndex.PartitionParent != parent.Address() {
		next, definition := *child, *child.SQLIndex
		definition.PartitionParent = parent.Address()
		next.SQLIndex = &definition
		next.ObjectRevision++
		if err := stage.Replace(&next); err != nil {
			return err
		}
		if parent.SQLIndex.Constraint != nil {
			table := view.MustRelation(child.SQLIndex.Parent)
			changed := *table
			changed.Constraints.Unique = slices.Clone(table.Constraints.Unique)
			changed.ObjectRevision++
			for i := range changed.Constraints.Unique {
				c := &changed.Constraints.Unique[i]
				if c.Index == child.Address() {
					c.Parent = parent.SQLIndex.Constraint.OID
					c.IsLocal = false
				}
			}
			if err := stage.Replace(&changed); err != nil {
				return err
			}
		}
	}
	return e.stagePartitionIndexValidity(stage, parent.Address())
}

func (s *StatementScope) partitionIndexTransaction(stmt parser.Statement) bool {
	if v, ok := stmt.(*parser.AlterIndex); ok {
		return v.Action == "attach"
	}
	drop, ok := stmt.(*parser.DropIndex)
	if !ok {
		return false
	}
	e := s.engine()
	e.mu.Lock()
	defer e.mu.Unlock()
	auth, err := e.statementAuthorizationLocked(s.op, stmt)
	if err != nil {
		return false
	}
	if len(drop.Names) == 0 {
		return false
	}
	for _, name := range drop.Names {
		ref, err := e.resolveRelationReference(auth, name, true)
		if err != nil {
			return false
		}
		index, ok := auth.Catalog.Relation(ref.Address)
		if !ok || index.SQLIndex == nil {
			return false
		}
		table, ok := auth.Catalog.Relation(index.SQLIndex.Parent)
		if !ok || table.Partition == nil && index.SQLIndex.PartitionParent == (ids.ObjectAddress{}) {
			return false
		}
	}
	return true
}
