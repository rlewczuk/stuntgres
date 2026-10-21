package engine

import (
	"slices"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/session"
)

func (e *Engine) stagePartitionIndexChild(op *session.Operation, stage *CatalogStage, table, parentIndex, child *catalog.Relation, allowIncompleteVirtual bool) (*catalog.Relation, error) {
	view := e.stageRead(stage)
	ctx := regobj.ACLItemOutputContext(op.Context(), regobj.ACLItemOutput{Owner: e.catalog.owner(), Roles: e.roles.read(e.catalog.owner(), view, e.operationRolesLocked(op))})
	if stage.Base.Get(parentIndex.Address()) != nil {
		stage.domainBases = append(stage.domainBases, parentIndex)
	}
	mapped, err := MapPartitionIndex(ctx, view, table, child, parentIndex.SQLIndex)
	if err != nil {
		return nil, err
	}
	mapped.PartitionParent = parentIndex.Address()
	mapped.Valid = true
	mapped.Constraint = parentIndex.SQLIndex.Constraint
	if err = PartitionUniqueCoverage(child, mapped); err != nil {
		return nil, err
	}
	mapped.Constraint = nil
	var candidates []*catalog.Relation
	for _, descriptor := range view.Descriptors() {
		index, ok := descriptor.(*catalog.Relation)
		if ok && index.SQLIndex != nil && index.SQLIndex.Parent == child.Address() && index.SQLIndex.PartitionParent == (ids.ObjectAddress{}) {
			candidates = append(candidates, index)
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
		match, err := PartitionIndexEquivalent(ctx, view, child, mapped, candidate.SQLIndex)
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
			if err = StagePartitionDescriptor(stage, &changed); err != nil {
				return nil, err
			}
		}
		if err = StagePartitionDescriptor(stage, &next); err != nil {
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
	oid, err := e.catalog.allocateOIDLocked()
	if err != nil {
		return nil, err
	}
	revision, err := e.catalog.revisionLocked()
	if err != nil {
		return nil, err
	}
	index := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid}, ObjectNamespace: child.Namespace(), ObjectName: name, ObjectRevision: revision, Owner: child.Owner, RelationKind: catalog.IndexRelation, SQLIndex: mapped, Columns: slices.Clone(parentIndex.Columns), Attributes: slices.Clone(parentIndex.Attributes), ColumnMeta: catalog.ColumnMetadata{HighWater: parentIndex.ColumnMeta.HighWater}, Index: make(map[string]int, len(parentIndex.Columns))}
	for i := range index.Columns {
		index.Columns[i].OriginTable = uint32(oid)
		index.Columns[i].OriginAttribute = int16(i + 1)
		index.Index[index.Columns[i].Name] = i
	}
	if backing := parentIndex.SQLIndex.Constraint; backing != nil {
		cid, err := e.catalog.allocateOIDLocked()
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
			c := catalog.ExclusionConstraint{OID: cid, Name: name, Index: index.Address(), Parent: backing.OID, Operators: mapped.Exclusion, Deferrable: source.Deferrable, InitiallyDeferred: source.InitiallyDeferred}
			changed.Constraints.Exclusion = append(slices.Clone(child.Constraints.Exclusion), c)
			mapped.Constraint = ExclusionBacking(&c)
		} else {
			c := catalog.UniqueConstraint{OID: cid, Name: name, Index: index.Address(), Parent: backing.OID, Primary: backing.Primary, NullsNotDistinct: mapped.NullsNotDistinct, Attributes: make([]int32, len(mapped.Keys))}
			for i, key := range mapped.Keys {
				c.Attributes[i] = key.Attribute
			}
			changed.Constraints.Unique = append(slices.Clone(child.Constraints.Unique), c)
			mapped.Constraint = UniqueBacking(&c)
		}
		if err = StagePartitionDescriptor(stage, &changed); err != nil {
			return nil, err
		}
		child = &changed
	}
	if err = stage.Create(index); err != nil {
		return nil, err
	}
	if child.Partition != nil {
		if err = e.stagePartitionIndexChildren(op, stage, child, index, allowIncompleteVirtual); err != nil {
			return nil, err
		}
	}
	return index, nil
}

func (e *Engine) stagePartitionIndexChildren(op *session.Operation, stage *CatalogStage, table, index *catalog.Relation, allowIncompleteVirtual bool) error {
	if original, ok := stage.Base.Relation(table.Address()); ok {
		stage.domainBases = append(stage.domainBases, original)
	}
	for _, address := range table.Partition.Children {
		child := e.stageRead(stage).MustRelation(address)
		if _, err := e.stagePartitionIndexChild(op, stage, table, index, child, allowIncompleteVirtual); err != nil {
			return err
		}
	}
	view := e.stageRead(stage)
	valid := PartitionIndexCoverageComplete(view, index)
	if index.SQLIndex.Valid != valid {
		next, definition := *index, *index.SQLIndex
		definition.Valid = valid
		next.SQLIndex = &definition
		next.ObjectRevision++
		return StagePartitionDescriptor(stage, &next)
	}
	return nil
}
