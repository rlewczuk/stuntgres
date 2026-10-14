package engine

import (
	"context"
	"fmt"
	"slices"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func partitionIndexCoverageComplete(view CatalogRead, index *relationDescriptor) bool {
	table, ok := view.state.descriptors[index.sqlIndex.parent].(*relationDescriptor)
	if !ok || table.partition == nil {
		return index.sqlIndex.valid
	}
	for _, child := range table.partition.children {
		found := false
		for _, descriptor := range view.state.descriptors {
			candidate, ok := descriptor.(*relationDescriptor)
			if ok && candidate.sqlIndex != nil && candidate.sqlIndex.partitionParent == index.address && candidate.sqlIndex.parent == child && candidate.sqlIndex.valid {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func (e *Engine) stagePartitionIndexValidity(stage *CatalogStage, promote ObjectAddress) error {
	// Only explicit ATTACH INDEX can promote that index and its ancestors.
	// Table edge changes preserve invalidity even when coverage becomes complete.
	// Each pass resolves another level; the catalog validator rejects cycles.
	view := e.stageRead(stage)
	for range len(view.state.descriptors) {
		changed := false
		for _, descriptor := range view.state.descriptors {
			index, ok := descriptor.(*relationDescriptor)
			if !ok || index.sqlIndex == nil {
				continue
			}
			table, ok := view.state.descriptors[index.sqlIndex.parent].(*relationDescriptor)
			if !ok || table.partition == nil {
				continue
			}
			valid := partitionIndexCoverageComplete(view, index)
			if valid && !index.sqlIndex.valid && !partitionIndexPromotionPath(view, promote, index.address) {
				continue
			}
			if index.sqlIndex.valid == valid {
				continue
			}
			if index.revision == DefinitionRevision(^uint64(0)) {
				return fmt.Errorf("stuntgres: definition revision exhausted")
			}
			next, definition := *index, *index.sqlIndex
			definition.valid = valid
			next.sqlIndex = &definition
			next.revision++
			if err := stagePartitionDescriptor(stage, &next); err != nil {
				return err
			}
			changed = true
		}
		if !changed {
			return nil
		}
		view = e.stageRead(stage)
	}
	return fmt.Errorf("stuntgres: cyclic partition index hierarchy")
}

func partitionIndexPromotionPath(view CatalogRead, start, target ObjectAddress) bool {
	for remaining := len(view.state.descriptors); start != (ObjectAddress{}) && remaining > 0; remaining-- {
		if start == target {
			return true
		}
		index, ok := view.state.descriptors[start].(*relationDescriptor)
		if !ok || index.sqlIndex == nil {
			return false
		}
		start = index.sqlIndex.partitionParent
	}
	return false
}

func partitionIndexAttachCheck(op *Operation, view CatalogRead, parent, child *relationDescriptor) error {
	diagnostic := func(code, detail string) error {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: fmt.Sprintf(`cannot attach index "%s" as a partition of index "%s"`, child.name, parent.name), Detail: detail}
	}
	table := view.state.descriptors[parent.sqlIndex.parent].(*relationDescriptor)
	childTable := view.state.descriptors[child.sqlIndex.parent].(*relationDescriptor)
	if table.partition == nil || childTable.membership == nil || childTable.membership.parent != table.address {
		return diagnostic("55000", fmt.Sprintf(`Index "%s" is not an index on any partition of table "%s".`, child.name, table.name))
	}
	for _, descriptor := range view.state.descriptors {
		index, ok := descriptor.(*relationDescriptor)
		if ok && index.sqlIndex != nil && index.address != child.address && index.sqlIndex.partitionParent == parent.address && index.sqlIndex.parent == childTable.address {
			return diagnostic("55000", fmt.Sprintf(`Another index is already attached for partition "%s".`, childTable.name))
		}
	}
	if child.sqlIndex.partitionParent == parent.address {
		return nil
	}
	if child.sqlIndex.partitionParent != (ObjectAddress{}) {
		return diagnostic("55000", fmt.Sprintf(`Index "%s" is already attached to another index.`, child.name))
	}
	mapped, err := mapPartitionIndex(op.Context(), view, table, childTable, parent.sqlIndex)
	if err != nil {
		return err
	}
	match, err := partitionIndexEquivalent(op.Context(), view, childTable, mapped, child.sqlIndex)
	if err != nil {
		return err
	}
	if !match {
		return diagnostic("42P17", "The index definitions do not match.")
	}
	if parent.sqlIndex.constraint != nil {
		if child.sqlIndex.constraint == nil || child.sqlIndex.constraint.kind != uniqueBackingKind {
			return diagnostic("42P17", fmt.Sprintf(`The index "%s" belongs to a constraint in table "%s" but no constraint exists for index "%s".`, parent.name, table.name, child.name))
		}
		if parent.sqlIndex.constraint.Primary {
			for _, key := range child.sqlIndex.keys {
				if childTable.columns[childTable.indexForAttribute(key.attribute)].Nullable {
					return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P16", Message: "invalid primary key definition", Detail: fmt.Sprintf(`Column "%s" of relation "%s" is not marked NOT NULL.`, childTable.columns[childTable.indexForAttribute(key.attribute)].Name, childTable.name)}
				}
			}
		}
	}
	return nil
}

func (e *Engine) stagePartitionIndexAttach(op *Operation, stage *CatalogStage, parent, child *relationDescriptor) error {
	view := e.stageRead(stage)
	if err := partitionIndexAttachCheck(op, view, parent, child); err != nil {
		return err
	}
	if child.sqlIndex.partitionParent != parent.address {
		next, definition := *child, *child.sqlIndex
		definition.partitionParent = parent.address
		next.sqlIndex = &definition
		next.revision++
		if err := stage.Replace(&next); err != nil {
			return err
		}
		if parent.sqlIndex.constraint != nil {
			table := view.state.descriptors[child.sqlIndex.parent].(*relationDescriptor)
			changed := *table
			changed.uniqueConstraints = slices.Clone(table.uniqueConstraints)
			changed.revision++
			for i := range changed.uniqueConstraints {
				c := &changed.uniqueConstraints[i]
				if c.Index == child.address {
					c.Parent = parent.sqlIndex.constraint.OID
					c.IsLocal = false
				}
			}
			if err := stage.Replace(&changed); err != nil {
				return err
			}
		}
	}
	return e.stagePartitionIndexValidity(stage, parent.address)
}

func validatePartitionIndex(view CatalogRead, index, table *relationDescriptor) error {
	d := index.sqlIndex
	if table.partition != nil && d.valid && !partitionIndexCoverageComplete(view, index) {
		return fmt.Errorf("stuntgres: inconsistent virtual index validity")
	}
	if err := partitionUniqueCoverage(table, d); err != nil {
		return err
	}
	if d.partitionParent == (ObjectAddress{}) {
		for _, c := range table.uniqueConstraints {
			if c.Index == index.address && c.Parent != 0 {
				return fmt.Errorf("stuntgres: orphan partition constraint ancestry")
			}
		}
		return nil
	}
	parent, ok := view.state.descriptors[d.partitionParent].(*relationDescriptor)
	if !ok || parent.sqlIndex == nil || table.membership == nil || parent.sqlIndex.parent != table.membership.parent {
		return fmt.Errorf("stuntgres: invalid partition index ancestry")
	}
	for _, descriptor := range view.state.descriptors {
		other, ok := descriptor.(*relationDescriptor)
		if ok && other.address != index.address && other.sqlIndex != nil && other.sqlIndex.partitionParent == d.partitionParent && other.sqlIndex.parent == table.address {
			return fmt.Errorf("stuntgres: duplicate partition index ancestry")
		}
	}
	parentTable := view.state.descriptors[parent.sqlIndex.parent].(*relationDescriptor)
	mapped, err := mapPartitionIndex(context.Background(), view, parentTable, table, parent.sqlIndex)
	if err != nil {
		return err
	}
	match, err := partitionIndexEquivalent(context.Background(), view, table, mapped, d)
	if err != nil {
		return err
	}
	if !match {
		return fmt.Errorf("stuntgres: inconsistent partition index definition")
	}
	remaining := len(view.state.descriptors)
	for address := d.partitionParent; address != (ObjectAddress{}); remaining-- {
		if remaining == 0 || address == index.address {
			return fmt.Errorf("stuntgres: cyclic partition index ancestry")
		}
		ancestor, ok := view.state.descriptors[address].(*relationDescriptor)
		if !ok || ancestor.sqlIndex == nil {
			return fmt.Errorf("stuntgres: orphan partition index")
		}
		address = ancestor.sqlIndex.partitionParent
	}
	if parent.sqlIndex.constraint != nil && d.constraint == nil {
		return fmt.Errorf("stuntgres: missing partition constraint backing")
	}
	if d.constraint != nil {
		expected := OID(0)
		if parent.sqlIndex.constraint != nil {
			expected = parent.sqlIndex.constraint.OID
		}
		found := false
		for _, c := range table.uniqueConstraints {
			if c.Index == index.address && c.Parent == expected && c.IsLocal == (expected == 0) {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("stuntgres: inconsistent partition constraint ancestry")
		}
	}
	return nil
}

func (s *StatementScope) partitionIndexTransaction(stmt parser.Statement) bool {
	if v, ok := stmt.(*parser.AlterIndex); ok {
		return v.Action == "attach"
	}
	drop, ok := stmt.(*parser.DropIndex)
	if !ok {
		return false
	}
	e := s.op.session.Engine()
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
		index, ok := auth.catalog.state.descriptors[ref.Address].(*relationDescriptor)
		if !ok || index.sqlIndex == nil {
			return false
		}
		table, ok := auth.catalog.state.descriptors[index.sqlIndex.parent].(*relationDescriptor)
		if !ok || table.partition == nil && index.sqlIndex.partitionParent == (ObjectAddress{}) {
			return false
		}
	}
	return true
}
