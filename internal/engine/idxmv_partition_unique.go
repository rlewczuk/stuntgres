package engine

import (
	"slices"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/regobj"
)

// StagePartitionIndexChild attaches or creates the index of partition child
// that corresponds to parentIndex of table, with its backing constraint, and
// recurses into a partitioned child.
func StagePartitionIndexChild(h index.Host, table, parentIndex, child *catalog.Relation, allowIncompleteVirtual bool) (*catalog.Relation, error) {
	view := h.StageRead()
	ctx := regobj.ACLItemOutputContext(h.Context(), h.ACLOutput(view))
	if h.StageBase().Get(parentIndex.Address()) != nil {
		h.AddDomainBase(parentIndex)
	}
	mapped, err := index.MapPartitionIndex(ctx, view, table, child, parentIndex.SQLIndex)
	if err != nil {
		return nil, err
	}
	mapped.PartitionParent = parentIndex.Address()
	mapped.Valid = true
	mapped.Constraint = parentIndex.SQLIndex.Constraint
	if err = index.PartitionUniqueCoverage(child, mapped); err != nil {
		return nil, err
	}
	mapped.Constraint = nil
	var candidates []*catalog.Relation
	for _, descriptor := range view.Descriptors() {
		existing, ok := descriptor.(*catalog.Relation)
		if ok && existing.SQLIndex != nil && existing.SQLIndex.Parent == child.Address() && existing.SQLIndex.PartitionParent == (ids.ObjectAddress{}) {
			candidates = append(candidates, existing)
		}
	}
	slices.SortFunc(candidates, func(a, b *catalog.Relation) int {
		if a.Address().ObjectID < b.Address().ObjectID {
			return -1
		}
		if a.Address().ObjectID > b.Address().ObjectID {
			return 1
		}
		return 0
	})
	for _, candidate := range candidates {
		// PostgreSQL 18.0 never adopts an independent exclusion backing.
		if parentIndex.SQLIndex.IsExclusion() {
			continue
		}
		// Recursive CREATE retains a reusable incomplete virtual tree. Table
		// ATTACH requires valid existing backings and must not heal an invalid
		// physical index left by a failed concurrent build.
		if !candidate.SQLIndex.Ready || !candidate.SQLIndex.Live || !candidate.SQLIndex.Valid && (child.Partition == nil || !allowIncompleteVirtual) {
			continue
		}
		if parentIndex.SQLIndex.Constraint != nil && (candidate.SQLIndex.Constraint == nil || candidate.SQLIndex.Constraint.Kind != catalog.UniqueBackingKind || candidate.SQLIndex.Constraint.Primary != parentIndex.SQLIndex.Constraint.Primary) {
			continue
		}
		match, err := index.PartitionIndexEquivalent(ctx, view, child, mapped, candidate.SQLIndex)
		if err != nil {
			return nil, err
		}
		if !match {
			continue
		}
		next, definition := *candidate, *candidate.SQLIndex
		definition.PartitionParent = parentIndex.Address()
		next.SQLIndex = &definition
		next.ObjectRevision++
		if parentIndex.SQLIndex.Constraint != nil {
			changed := *child
			changed.Constraints.Unique = slices.Clone(child.Constraints.Unique)
			for i := range changed.Constraints.Unique {
				c := &changed.Constraints.Unique[i]
				if c.Index == candidate.Address() {
					c.Parent = parentIndex.SQLIndex.Constraint.OID
					c.IsLocal = false
				}
			}
			if err = index.StagePartitionDescriptor(h.DescriptorStage(), &changed); err != nil {
				return nil, err
			}
		}
		if err = index.StagePartitionDescriptor(h.DescriptorStage(), &next); err != nil {
			return nil, err
		}
		return &next, nil
	}
	labels := make([]string, len(parentIndex.Columns))
	for i, c := range parentIndex.Columns {
		labels[i] = c.Name
	}
	suffix := "idx"
	addition := strings.Join(labels, "_")
	used := catalog.ConstraintNames(view, child.Namespace(), child)
	for key := range view.Names() {
		if key.ClassID == ids.RelationClassID && key.Namespace == child.Namespace() {
			used[key.Name] = true
		}
	}
	if parentIndex.SQLIndex.Constraint != nil {
		suffix = "key"
		if parentIndex.SQLIndex.IsExclusion() {
			suffix = "excl"
		}
		if parentIndex.SQLIndex.Constraint.Primary {
			suffix = "pkey"
			addition = ""
		}
	}
	name := catalog.GeneratedConstraintName(child.Name(), addition, suffix, used)
	oid, err := h.AllocateOID()
	if err != nil {
		return nil, err
	}
	revision, err := h.Revision()
	if err != nil {
		return nil, err
	}
	idx := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid}, ObjectNamespace: child.Namespace(), ObjectName: name, ObjectRevision: revision, Owner: child.Owner, RelationKind: catalog.IndexRelation, SQLIndex: mapped, Columns: slices.Clone(parentIndex.Columns), Attributes: slices.Clone(parentIndex.Attributes), ColumnMeta: catalog.ColumnMetadata{HighWater: parentIndex.ColumnMeta.HighWater}, Index: make(map[string]int, len(parentIndex.Columns))}
	for i := range idx.Columns {
		idx.Columns[i].OriginTable = uint32(oid)
		idx.Columns[i].OriginAttribute = int16(i + 1)
		idx.Index[idx.Columns[i].Name] = i
	}
	if backing := parentIndex.SQLIndex.Constraint; backing != nil {
		cid, err := h.AllocateOID()
		if err != nil {
			return nil, err
		}
		changed := *child
		if parentIndex.SQLIndex.IsExclusion() {
			var source catalog.ExclusionConstraint
			for _, c := range table.Constraints.Exclusion {
				if c.OID == backing.OID {
					source = c
					break
				}
			}
			c := catalog.ExclusionConstraint{OID: cid, Name: name, Index: idx.Address(), Parent: backing.OID, Operators: mapped.Exclusion, Deferrable: source.Deferrable, InitiallyDeferred: source.InitiallyDeferred}
			changed.Constraints.Exclusion = append(slices.Clone(child.Constraints.Exclusion), c)
			mapped.Constraint = index.ExclusionBacking(&c)
		} else {
			c := catalog.UniqueConstraint{OID: cid, Name: name, Index: idx.Address(), Parent: backing.OID, Primary: backing.Primary, NullsNotDistinct: mapped.NullsNotDistinct, Attributes: make([]int32, len(mapped.Keys))}
			for i, key := range mapped.Keys {
				c.Attributes[i] = key.Attribute
			}
			changed.Constraints.Unique = append(slices.Clone(child.Constraints.Unique), c)
			mapped.Constraint = index.UniqueBacking(&c)
		}
		if err = index.StagePartitionDescriptor(h.DescriptorStage(), &changed); err != nil {
			return nil, err
		}
		child = &changed
	}
	if err = h.Stage().Create(idx); err != nil {
		return nil, err
	}
	if child.Partition != nil {
		if err = StagePartitionIndexChildren(h, child, idx, allowIncompleteVirtual); err != nil {
			return nil, err
		}
	}
	return idx, nil
}

// StagePartitionIndexChildren stages the child indexes of idx on every
// partition of table, then records whether idx covers them all.
func StagePartitionIndexChildren(h index.Host, table, idx *catalog.Relation, allowIncompleteVirtual bool) error {
	if original, ok := h.StageBase().Relation(table.Address()); ok {
		h.AddDomainBase(original)
	}
	for _, address := range table.Partition.Children {
		child := h.StageRead().MustRelation(address)
		if _, err := StagePartitionIndexChild(h, table, idx, child, allowIncompleteVirtual); err != nil {
			return err
		}
	}
	view := h.StageRead()
	valid := index.PartitionIndexCoverageComplete(view, idx)
	if idx.SQLIndex.Valid != valid {
		next, definition := *idx, *idx.SQLIndex
		definition.Valid = valid
		next.SQLIndex = &definition
		next.ObjectRevision++
		return index.StagePartitionDescriptor(h.DescriptorStage(), &next)
	}
	return nil
}
