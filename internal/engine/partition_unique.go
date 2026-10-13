package engine

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func partitionUniqueCoverage(table *relationDescriptor, index *indexDefinition) error {
	if table.partition == nil || !index.unique {
		return nil
	}
	kind := "UNIQUE"
	if index.constraint != nil && index.constraint.Primary {
		kind = "PRIMARY KEY"
	}
	for _, partitionKey := range table.partition.keys {
		if partitionKey.derived != nil {
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "unsupported " + kind + " constraint with partition key definition", Detail: kind + " constraints cannot be used when partition keys include expressions."}
		}
		found := false
		for _, key := range index.keys {
			if key.attribute != partitionKey.attribute || key.derived != nil {
				continue
			}
			usage := indexKeyCollation(table.columns[table.indexForAttribute(key.attribute)].Usage, key)
			class := key.class
			if class.Name == "" {
				class, _ = sqltype.DefaultIndexClass(usage.Type)
			}
			comparison, err := sqltype.BindIndexOrdering(usage, class)
			found = err == nil && usage.Collation == partitionKey.usage.Collation && comparison == partitionKey.comparison
			if found {
				break
			}
		}
		if !found {
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "unique constraint on partitioned table must include all partitioning columns", Detail: fmt.Sprintf(`%s constraint on table "%s" lacks column "%s" which is part of the partition key.`, kind, table.name, partitionKey.label)}
		}
	}
	return nil
}

// Mapping uses the same cooked-expression path as LIKE; inherited providers are
// never resolved again by name. Sort presentation is not attachment identity.
func mapPartitionIndex(ctx context.Context, view CatalogRead, source, child *relationDescriptor, old *indexDefinition) (*indexDefinition, error) {
	in := createLikeInput{source: source, targetSlots: make([]int, len(source.columns))}
	for i, column := range source.columns {
		slot, ok := child.index[column.Name]
		if !ok {
			return nil, fmt.Errorf("stuntgres: missing partition index column")
		}
		in.targetSlots[i] = slot
	}
	d := *old
	d.parent, d.partitionParent, d.constraint = child.address, ObjectAddress{}, nil
	d.keys, d.include = slices.Clone(old.keys), slices.Clone(old.include)
	for i := range d.include {
		a, err := in.attribute(child, d.include[i])
		if err != nil {
			return nil, err
		}
		d.include[i] = a
	}
	for i := range d.keys {
		key := &d.keys[i]
		if key.derived == nil {
			a, err := in.attribute(child, key.attribute)
			if err != nil {
				return nil, err
			}
			key.attribute = a
			continue
		}
		node, err := in.remap(key.derived.definition, child, view)
		if err != nil {
			return nil, err
		}
		execution, err := in.remap(key.derived.execution, child, view)
		if err != nil {
			return nil, err
		}
		derived := &derivedIndexKey{definition: node, execution: execution, attributes: child.attributes}
		if _, err = (scalarRenderer{owner: child, dependencies: &derived.dependencies}).sql(ctx, view, node); err != nil {
			return nil, err
		}
		key.derived = derived
	}
	if old.predicate != nil {
		node, err := in.remap(old.predicate.definition, child, view)
		if err != nil {
			return nil, err
		}
		execution, err := in.remap(old.predicate.execution, child, view)
		if err != nil {
			return nil, err
		}
		predicate := &indexPredicate{definition: node, execution: execution, attributes: child.attributes}
		if _, err = (scalarRenderer{owner: child, dependencies: &predicate.dependencies}).sql(ctx, view, node); err != nil {
			return nil, err
		}
		d.predicate = predicate
	}
	return &d, nil
}

func partitionIndexEquivalent(ctx context.Context, view CatalogRead, table *relationDescriptor, a, b *indexDefinition) (bool, error) {
	if a.method != b.method || a.unique != b.unique || a.nullsNotDistinct != b.nullsNotDistinct || len(a.keys) != len(b.keys) || !slices.Equal(a.include, b.include) {
		return false, nil
	}
	render := func(expr BoundExpr) (string, error) { return (scalarRenderer{owner: table}).sql(ctx, view, expr) }
	for i, key := range a.keys {
		other := b.keys[i]
		if key.attribute != other.attribute || (key.derived == nil) != (other.derived == nil) {
			return false, nil
		}
		au, err := indexKeyUsage(table, key)
		if err != nil {
			return false, err
		}
		bu, err := indexKeyUsage(table, other)
		if err != nil {
			return false, err
		}
		if indexKeyCollation(au, key).Collation != indexKeyCollation(bu, other).Collation {
			return false, nil
		}
		ac, bc := key.class, other.class
		if ac.Name == "" {
			ac, _ = sqltype.DefaultIndexClass(au.Type)
		}
		if bc.Name == "" {
			bc, _ = sqltype.DefaultIndexClass(bu.Type)
		}
		if ac.Family != bc.Family {
			return false, nil
		}
		if key.derived != nil {
			left, err := render(key.derived.definition)
			if err != nil {
				return false, err
			}
			right, err := render(other.derived.definition)
			if err != nil {
				return false, err
			}
			if left != right {
				return false, nil
			}
		}
	}
	if (a.predicate == nil) != (b.predicate == nil) {
		return false, nil
	}
	if a.predicate != nil {
		left, err := render(a.predicate.definition)
		if err != nil {
			return false, err
		}
		right, err := render(b.predicate.definition)
		if err != nil {
			return false, err
		}
		if left != right {
			return false, nil
		}
	}
	return true, nil
}

func stagePartitionDescriptor(stage *CatalogStage, d *relationDescriptor) error {
	if stage.base.state.descriptors[d.address] == nil {
		stage.descriptors[d.address] = d
		return nil
	}
	return stage.Replace(d)
}

func (e *Engine) stagePartitionIndexChild(op *Operation, stage *CatalogStage, table, parentIndex, child *relationDescriptor, allowIncompleteVirtual bool) (*relationDescriptor, error) {
	view := e.stageRead(stage)
	if stage.base.state.descriptors[parentIndex.address] != nil {
		stage.domainBases = append(stage.domainBases, parentIndex)
	}
	mapped, err := mapPartitionIndex(op.Context(), view, table, child, parentIndex.sqlIndex)
	if err != nil {
		return nil, err
	}
	mapped.partitionParent = parentIndex.address
	mapped.valid = true
	mapped.constraint = parentIndex.sqlIndex.constraint
	if err = partitionUniqueCoverage(child, mapped); err != nil {
		return nil, err
	}
	mapped.constraint = nil
	var candidates []*relationDescriptor
	for _, descriptor := range view.state.descriptors {
		index, ok := descriptor.(*relationDescriptor)
		if ok && index.sqlIndex != nil && index.sqlIndex.parent == child.address && index.sqlIndex.partitionParent == (ObjectAddress{}) {
			candidates = append(candidates, index)
		}
	}
	slices.SortFunc(candidates, func(a, b *relationDescriptor) int {
		if a.address.ObjectID < b.address.ObjectID {
			return -1
		}
		if a.address.ObjectID > b.address.ObjectID {
			return 1
		}
		return 0
	})
	for _, candidate := range candidates {
		// Recursive CREATE retains a reusable incomplete virtual tree. Table
		// ATTACH requires valid existing backings and must not heal an invalid
		// physical index left by a failed concurrent build.
		if !candidate.sqlIndex.ready || !candidate.sqlIndex.live || !candidate.sqlIndex.valid && (child.partition == nil || !allowIncompleteVirtual) {
			continue
		}
		if parentIndex.sqlIndex.constraint != nil && (candidate.sqlIndex.constraint == nil || candidate.sqlIndex.constraint.kind != uniqueBackingKind || candidate.sqlIndex.constraint.Primary != parentIndex.sqlIndex.constraint.Primary) {
			continue
		}
		match, err := partitionIndexEquivalent(op.Context(), view, child, mapped, candidate.sqlIndex)
		if err != nil {
			return nil, err
		}
		if !match {
			continue
		}
		next, definition := *candidate, *candidate.sqlIndex
		definition.partitionParent = parentIndex.address
		next.sqlIndex = &definition
		next.revision++
		if parentIndex.sqlIndex.constraint != nil {
			changed := *child
			changed.uniqueConstraints = slices.Clone(child.uniqueConstraints)
			for i := range changed.uniqueConstraints {
				c := &changed.uniqueConstraints[i]
				if c.Index == candidate.address {
					c.Parent = parentIndex.sqlIndex.constraint.OID
					c.IsLocal = false
				}
			}
			if err = stagePartitionDescriptor(stage, &changed); err != nil {
				return nil, err
			}
		}
		if err = stagePartitionDescriptor(stage, &next); err != nil {
			return nil, err
		}
		return &next, nil
	}
	labels := make([]string, len(parentIndex.columns))
	for i, c := range parentIndex.columns {
		labels[i] = c.Name
	}
	suffix := "idx"
	addition := strings.Join(labels, "_")
	used := constraintNames(view, child.namespace, child)
	for key := range view.state.names {
		if key.ClassID == RelationClassID && key.Namespace == child.namespace {
			used[key.Name] = true
		}
	}
	if parentIndex.sqlIndex.constraint != nil {
		suffix = "key"
		if parentIndex.sqlIndex.constraint.Primary {
			suffix = "pkey"
			addition = ""
		}
	}
	name := generatedConstraintName(child.name, addition, suffix, used)
	oid, err := e.allocateOIDLocked()
	if err != nil {
		return nil, err
	}
	revision, err := e.revisionLocked()
	if err != nil {
		return nil, err
	}
	index := &relationDescriptor{address: ObjectAddress{ClassID: RelationClassID, ObjectID: oid}, namespace: child.namespace, name: name, revision: revision, owner: child.owner, relationKind: indexRelation, sqlIndex: mapped, columns: slices.Clone(parentIndex.columns), attributes: slices.Clone(parentIndex.attributes), attributeHighWater: parentIndex.attributeHighWater, index: make(map[string]int, len(parentIndex.columns))}
	for i := range index.columns {
		index.columns[i].OriginTable = uint32(oid)
		index.columns[i].OriginAttribute = int16(i + 1)
		index.index[index.columns[i].Name] = i
	}
	if backing := parentIndex.sqlIndex.constraint; backing != nil {
		cid, err := e.allocateOIDLocked()
		if err != nil {
			return nil, err
		}
		c := uniqueConstraint{OID: cid, Name: name, Index: index.address, Parent: backing.OID, Primary: backing.Primary, NullsNotDistinct: mapped.nullsNotDistinct, Attributes: make([]int32, len(mapped.keys))}
		for i, key := range mapped.keys {
			c.Attributes[i] = key.attribute
		}
		changed := *child
		changed.uniqueConstraints = append(slices.Clone(child.uniqueConstraints), c)
		if err = stagePartitionDescriptor(stage, &changed); err != nil {
			return nil, err
		}
		mapped.constraint = uniqueBacking(&c)
	}
	if err = stage.Create(index); err != nil {
		return nil, err
	}
	if child.partition != nil {
		if err = e.stagePartitionIndexChildren(op, stage, child, index, allowIncompleteVirtual); err != nil {
			return nil, err
		}
	}
	return index, nil
}

func (e *Engine) stagePartitionIndexChildren(op *Operation, stage *CatalogStage, table, index *relationDescriptor, allowIncompleteVirtual bool) error {
	if stage.base.state.descriptors[table.address] != nil {
		stage.domainBases = append(stage.domainBases, table)
	}
	for _, address := range table.partition.children {
		child := e.stageRead(stage).state.descriptors[address].(*relationDescriptor)
		if _, err := e.stagePartitionIndexChild(op, stage, table, index, child, allowIncompleteVirtual); err != nil {
			return err
		}
	}
	view := e.stageRead(stage)
	valid := partitionIndexValid(view, index)
	if index.sqlIndex.valid != valid {
		next, definition := *index, *index.sqlIndex
		definition.valid = valid
		next.sqlIndex = &definition
		next.revision++
		return stagePartitionDescriptor(stage, &next)
	}
	return nil
}

// Inline inherited indexes exist before the child's local declarations on
// replay. Translate immediate constraint identities in place so neither stored
// local-first slices nor collision-reminted backing OIDs change that order.
func appendPartitionInlineIndexes(view CatalogRead, table *relationDescriptor, indexes []*relationDescriptor) []*relationDescriptor {
	if table.membership != nil {
		start := len(indexes)
		parent := view.state.descriptors[table.membership.parent].(*relationDescriptor)
		indexes = appendPartitionInlineIndexes(view, parent, indexes)
		end := len(indexes)
		write := start
		for _, inherited := range indexes[start:end] {
			for _, constraint := range table.uniqueConstraints {
				if constraint.Parent == inherited.sqlIndex.constraint.OID {
					indexes[write] = view.state.descriptors[constraint.Index].(*relationDescriptor)
					write++
					break
				}
			}
		}
		indexes = indexes[:write]
	}
	for _, constraint := range table.uniqueConstraints {
		index := view.state.descriptors[constraint.Index].(*relationDescriptor)
		if constraint.Parent == 0 && !usingIndexDump(index) {
			indexes = append(indexes, index)
		}
	}
	return indexes
}

// This is the declaration order that creates automatic partition backings:
// inherited inline keys, local inline keys, then separately emitted indexes.
// Only the separate-index tail uses sparse collision provenance / OID ordering.
func partitionIndexDeclarationOrder(view CatalogRead, table *relationDescriptor) []*relationDescriptor {
	indexes := appendPartitionInlineIndexes(view, table, nil)
	inline := len(indexes)
	for _, descriptor := range view.state.descriptors {
		index, ok := descriptor.(*relationDescriptor)
		if !ok || index.sqlIndex == nil || index.sqlIndex.parent != table.address {
			continue
		}
		if backing := index.sqlIndex.constraint; backing != nil && backing.kind == uniqueBackingKind && !usingIndexDump(index) {
			continue
		}
		indexes = append(indexes, index)
	}
	slices.SortFunc(indexes[inline:], func(a, b *relationDescriptor) int {
		priority := func(index *relationDescriptor) OID {
			if source := view.state.dumpPriorities[index.address]; source != 0 {
				return source
			}
			return index.address.ObjectID
		}
		left, right := priority(a), priority(b)
		if left < right {
			return -1
		}
		if left > right {
			return 1
		}
		if a.address.ObjectID < b.address.ObjectID {
			return -1
		}
		if a.address.ObjectID > b.address.ObjectID {
			return 1
		}
		return 0
	})
	return indexes
}
