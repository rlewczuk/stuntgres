package engine

import (
	"context"
	"fmt"
	"slices"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func partitionIndexCoverageComplete(view CatalogRead, index *relationDescriptor) bool {
	return (&dropProvider{base: view}).partitionIndexCoverageComplete(index)
}

func (provider *dropProvider) partitionIndexCoverageComplete(index *relationDescriptor) bool {
	table, ok := provider.liveDescriptor(index.SQLIndex.Parent).(*relationDescriptor)
	if !ok || table.Partition == nil {
		return index.SQLIndex.Valid
	}
	for _, child := range table.Partition.Children {
		found := false
		for address := range provider.descriptors() {
			if provider.removed(address) {
				continue
			}
			candidate, ok := provider.liveDescriptor(address).(*relationDescriptor)
			if ok && candidate.SQLIndex != nil && candidate.SQLIndex.PartitionParent == index.address && candidate.SQLIndex.Parent == child && candidate.SQLIndex.Valid {
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

func (e *Engine) stagePartitionIndexValidity(stage *CatalogStage, promote ids.ObjectAddress) error {
	return stageDropProvider(context.Background(), stage, nil, nil, nil).partitionIndexValidity(promote)
}

func (provider *dropProvider) partitionIndexValidity(promote ids.ObjectAddress) error {
	// Only explicit ATTACH INDEX can promote that index and its ancestors.
	// Table edge changes preserve invalidity even when coverage becomes complete.
	// Each pass resolves another level; the catalog validator rejects cycles.
	for range provider.descriptorCount() {
		changed := false
		for address := range provider.descriptors() {
			if provider.removed(address) {
				continue
			}
			descriptor := provider.liveDescriptor(address)
			index, ok := descriptor.(*relationDescriptor)
			if !ok || index.SQLIndex == nil {
				continue
			}
			table, ok := provider.liveDescriptor(index.SQLIndex.Parent).(*relationDescriptor)
			if !ok || table.Partition == nil {
				continue
			}
			valid := provider.partitionIndexCoverageComplete(index)
			if valid && !index.SQLIndex.Valid && !provider.partitionIndexPromotionPath(promote, index.address) {
				continue
			}
			if index.SQLIndex.Valid == valid {
				continue
			}
			if index.revision == ids.DefinitionRevision(^uint64(0)) {
				return fmt.Errorf("stuntgres: definition revision exhausted")
			}
			next, definition := *index, *index.SQLIndex
			definition.Valid = valid
			next.SQLIndex = &definition
			next.revision++
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

func partitionIndexPromotionPath(view CatalogRead, start, target ids.ObjectAddress) bool {
	return (&dropProvider{base: view}).partitionIndexPromotionPath(start, target)
}

func (provider *dropProvider) partitionIndexPromotionPath(start, target ids.ObjectAddress) bool {
	for remaining := provider.descriptorCount(); start != (ids.ObjectAddress{}) && remaining > 0; remaining-- {
		if start == target {
			return true
		}
		index, ok := provider.liveDescriptor(start).(*relationDescriptor)
		if !ok || index.SQLIndex == nil {
			return false
		}
		start = index.SQLIndex.PartitionParent
	}
	return false
}

func partitionIndexAttachCheck(op *Operation, view CatalogRead, parent, child *relationDescriptor) error {
	diagnostic := func(code, detail string) error {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: fmt.Sprintf(`cannot attach index "%s" as a partition of index "%s"`, child.name, parent.name), Detail: detail}
	}
	table := view.MustRelation(parent.SQLIndex.Parent)
	childTable := view.MustRelation(child.SQLIndex.Parent)
	if table.Partition == nil || childTable.Membership == nil || childTable.Membership.Parent != table.address {
		return diagnostic("55000", fmt.Sprintf(`Index "%s" is not an index on any partition of table "%s".`, child.name, table.name))
	}
	for _, descriptor := range view.Descriptors() {
		index, ok := descriptor.(*relationDescriptor)
		if ok && index.SQLIndex != nil && index.address != child.address && index.SQLIndex.PartitionParent == parent.address && index.SQLIndex.Parent == childTable.address {
			return diagnostic("55000", fmt.Sprintf(`Another index is already attached for partition "%s".`, childTable.name))
		}
	}
	if child.SQLIndex.PartitionParent == parent.address {
		return nil
	}
	if child.SQLIndex.PartitionParent != (ids.ObjectAddress{}) {
		return diagnostic("55000", fmt.Sprintf(`Index "%s" is already attached to another index.`, child.name))
	}
	if parent.SQLIndex.isExclusion() || child.SQLIndex.isExclusion() {
		return diagnostic("42P17", "The index definitions do not match.")
	}
	mapped, err := mapPartitionIndex(op.Context(), view, table, childTable, parent.SQLIndex)
	if err != nil {
		return err
	}
	match, err := partitionIndexEquivalent(op.Context(), view, childTable, mapped, child.SQLIndex)
	if err != nil {
		return err
	}
	if !match {
		return diagnostic("42P17", "The index definitions do not match.")
	}
	if parent.SQLIndex.Constraint != nil {
		if child.SQLIndex.Constraint == nil || child.SQLIndex.Constraint.Kind != uniqueBackingKind {
			return diagnostic("42P17", fmt.Sprintf(`The index "%s" belongs to a constraint in table "%s" but no constraint exists for index "%s".`, parent.name, table.name, child.name))
		}
		if parent.SQLIndex.Constraint.Primary {
			for _, key := range child.SQLIndex.Keys {
				if childTable.Columns[childTable.indexForAttribute(key.Attribute)].Nullable {
					return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P16", Message: "invalid primary key definition", Detail: fmt.Sprintf(`Column "%s" of relation "%s" is not marked NOT NULL.`, childTable.Columns[childTable.indexForAttribute(key.Attribute)].Name, childTable.name)}
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
	if child.SQLIndex.PartitionParent != parent.address {
		next, definition := *child, *child.SQLIndex
		definition.PartitionParent = parent.address
		next.SQLIndex = &definition
		next.revision++
		if err := stage.Replace(&next); err != nil {
			return err
		}
		if parent.SQLIndex.Constraint != nil {
			table := view.MustRelation(child.SQLIndex.Parent)
			changed := *table
			changed.UniqueConstraints = slices.Clone(table.UniqueConstraints)
			changed.revision++
			for i := range changed.UniqueConstraints {
				c := &changed.UniqueConstraints[i]
				if c.Index == child.address {
					c.Parent = parent.SQLIndex.Constraint.OID
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
	d := index.SQLIndex
	if table.Partition != nil && d.Valid && !partitionIndexCoverageComplete(view, index) {
		return fmt.Errorf("stuntgres: inconsistent virtual index validity")
	}
	if err := partitionUniqueCoverage(table, d); err != nil {
		return err
	}
	if d.PartitionParent == (ids.ObjectAddress{}) {
		for _, c := range table.UniqueConstraints {
			if c.Index == index.address && c.Parent != 0 {
				return fmt.Errorf("stuntgres: orphan partition constraint ancestry")
			}
		}
		for _, c := range table.ExclusionConstraints {
			if c.Index == index.address && c.Parent != 0 {
				return fmt.Errorf("stuntgres: orphan partition exclusion ancestry")
			}
		}
		return nil
	}
	parent, ok := view.Relation(d.PartitionParent)
	if !ok || parent.SQLIndex == nil || table.Membership == nil || parent.SQLIndex.Parent != table.Membership.Parent {
		return fmt.Errorf("stuntgres: invalid partition index ancestry")
	}
	for _, descriptor := range view.Descriptors() {
		other, ok := descriptor.(*relationDescriptor)
		if ok && other.address != index.address && other.SQLIndex != nil && other.SQLIndex.PartitionParent == d.PartitionParent && other.SQLIndex.Parent == table.address {
			return fmt.Errorf("stuntgres: duplicate partition index ancestry")
		}
	}
	parentTable := view.MustRelation(parent.SQLIndex.Parent)
	mapped, err := mapPartitionIndex(context.Background(), view, parentTable, table, parent.SQLIndex)
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
	remaining := view.DescriptorCount()
	for address := d.PartitionParent; address != (ids.ObjectAddress{}); remaining-- {
		if remaining == 0 || address == index.address {
			return fmt.Errorf("stuntgres: cyclic partition index ancestry")
		}
		ancestor, ok := view.Relation(address)
		if !ok || ancestor.SQLIndex == nil {
			return fmt.Errorf("stuntgres: orphan partition index")
		}
		address = ancestor.SQLIndex.PartitionParent
	}
	if parent.SQLIndex.Constraint != nil && d.Constraint == nil {
		return fmt.Errorf("stuntgres: missing partition constraint backing")
	}
	if d.Constraint != nil {
		expected := ids.OID(0)
		if parent.SQLIndex.Constraint != nil {
			expected = parent.SQLIndex.Constraint.OID
		}
		found := false
		for _, c := range table.UniqueConstraints {
			if c.Index == index.address && c.Parent == expected && c.IsLocal == (expected == 0) {
				found = true
			}
		}
		for _, c := range table.ExclusionConstraints {
			if c.Index == index.address && c.Parent == expected && c.IsLocal == (expected == 0) {
				source := exclusionDefinition(parentTable, parent)
				found = source != nil && c.Deferrable == source.Deferrable && c.InitiallyDeferred == source.InitiallyDeferred
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
		index, ok := auth.catalog.Relation(ref.Address)
		if !ok || index.SQLIndex == nil {
			return false
		}
		table, ok := auth.catalog.Relation(index.SQLIndex.Parent)
		if !ok || table.Partition == nil && index.SQLIndex.PartitionParent == (ids.ObjectAddress{}) {
			return false
		}
	}
	return true
}
