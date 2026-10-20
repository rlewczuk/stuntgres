package engine

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func partitionUniqueCoverage(table *catalog.Relation, index *catalog.IndexDefinition) error {
	if table.Partition == nil || !index.Unique && !index.IsExclusion() {
		return nil
	}
	kind := "UNIQUE"
	if index.Constraint != nil && index.Constraint.Primary {
		kind = "PRIMARY KEY"
	}
	if index.IsExclusion() {
		kind = "EXCLUDE"
	}
	for _, partitionKey := range table.Partition.Keys {
		if partitionKey.Derived != nil {
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "unsupported " + kind + " constraint with partition key definition", Detail: kind + " constraints cannot be used when partition keys include expressions."}
		}
		found := false
		for i, key := range index.Keys {
			if key.Attribute != partitionKey.Attribute || key.Derived != nil {
				continue
			}
			usage := indexKeyCollation(table.Columns[table.IndexForAttribute(key.Attribute)].Usage, key)
			if index.IsExclusion() {
				if usage.Collation != partitionKey.Usage.Collation {
					continue
				}
				operator := index.Exclusion[i]
				if !operator.IsEquality() {
					return sqlError("0A000", fmt.Sprintf(`cannot match partition key to index on column "%s" using non-equal operator "%s"`, partitionKey.Label, operator.Name))
				}
				inputs, err := operator.OperatorInputs()
				equality, bindErr := sqltype.BindExclusionOperator(partitionKey.Usage, partitionKey.Class, "=")
				expected, inputErr := equality.OperatorInputs()
				found = err == nil && bindErr == nil && inputErr == nil && operator.Name == equality.Name && inputs == expected
				if found {
					break
				}
				continue
			}
			class := key.Class
			if class.Name == "" {
				class, _ = sqltype.DefaultIndexClass(usage.Type)
			}
			comparison, err := sqltype.BindIndexOrdering(usage, class)
			partitionComparison := partitionKey.Comparison
			if table.Partition.Strategy == parser.PartitionHash {
				equalityClass, ok := sqltype.LookupIndexClass(partitionKey.Class.Name)
				if !ok {
					continue
				}
				var equalityErr error
				partitionComparison, equalityErr = sqltype.BindIndexOrdering(partitionKey.Usage, equalityClass)
				if equalityErr != nil {
					continue
				}
			}
			found = err == nil && usage.Collation == partitionKey.Usage.Collation && comparison == partitionComparison
			if found {
				break
			}
		}
		if !found {
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "unique constraint on partitioned table must include all partitioning columns", Detail: fmt.Sprintf(`%s constraint on table "%s" lacks column "%s" which is part of the partition key.`, kind, table.Name(), partitionKey.Label)}
		}
	}
	return nil
}

// Mapping uses the same cooked-expression path as LIKE; inherited providers are
// never resolved again by name. Sort presentation is not attachment identity.
func mapPartitionIndex(ctx context.Context, view catalog.Read, source, child *catalog.Relation, old *catalog.IndexDefinition) (*catalog.IndexDefinition, error) {
	in := createLikeInput{source: source, targetSlots: make([]int, len(source.Columns))}
	for i, column := range source.Columns {
		slot, ok := child.Index[column.Name]
		if !ok {
			return nil, fmt.Errorf("stuntgres: missing partition index column")
		}
		in.targetSlots[i] = slot
	}
	d := *old
	d.Parent, d.PartitionParent, d.Constraint = child.Address(), ids.ObjectAddress{}, nil
	d.Keys, d.Include = slices.Clone(old.Keys), slices.Clone(old.Include)
	for i := range d.Include {
		a, err := in.attribute(child, d.Include[i])
		if err != nil {
			return nil, err
		}
		d.Include[i] = a
	}
	for i := range d.Keys {
		key := &d.Keys[i]
		if key.Derived == nil {
			a, err := in.attribute(child, key.Attribute)
			if err != nil {
				return nil, err
			}
			key.Attribute = a
			continue
		}
		node, err := remapLikeExpression(&in, key.Derived.Definition, child, view)
		if err != nil {
			return nil, err
		}
		execution, err := remapLikeExpression(&in, key.Derived.Execution, child, view)
		if err != nil {
			return nil, err
		}
		derived := &catalog.DerivedIndexKey{Definition: node, Execution: execution, Attributes: child.Attributes}
		if _, err = (scalarRenderer{validation: true, owner: child, dependencies: &derived.Dependencies}).sql(ctx, view, node); err != nil {
			return nil, err
		}
		key.Derived = derived
	}
	if old.Predicate != nil {
		node, err := remapLikeExpression(&in, old.Predicate.Definition, child, view)
		if err != nil {
			return nil, err
		}
		execution, err := remapLikeExpression(&in, old.Predicate.Execution, child, view)
		if err != nil {
			return nil, err
		}
		predicate := &catalog.IndexPredicate{Definition: node, Execution: execution, Attributes: child.Attributes}
		if _, err = (scalarRenderer{validation: true, owner: child, dependencies: &predicate.Dependencies}).sql(ctx, view, node); err != nil {
			return nil, err
		}
		d.Predicate = predicate
	}
	if old.IsExclusion() {
		var err error
		d.Exclusion, err = rebindExclusionOperators(child, &d)
		if err != nil {
			return nil, err
		}
	}
	return &d, nil
}

func partitionIndexEquivalent(ctx context.Context, view catalog.Read, table *catalog.Relation, a, b *catalog.IndexDefinition) (bool, error) {
	if a.Method != b.Method || a.Unique != b.Unique || a.NullsNotDistinct != b.NullsNotDistinct || len(a.Keys) != len(b.Keys) || !slices.Equal(a.Include, b.Include) {
		return false, nil
	}
	if len(a.Exclusion) != len(b.Exclusion) {
		return false, nil
	}
	for i, operator := range a.Exclusion {
		other := b.Exclusion[i]
		inputs, err := operator.OperatorInputs()
		if err != nil {
			return false, err
		}
		otherInputs, err := other.OperatorInputs()
		if err != nil {
			return false, err
		}
		if operator.Name != other.Name || inputs != otherInputs || operator.IsEquality() != other.IsEquality() {
			return false, nil
		}
	}
	render := func(expr expr.Expr) (string, error) { return (scalarRenderer{owner: table}).sql(ctx, view, expr) }
	for i, key := range a.Keys {
		other := b.Keys[i]
		if key.Attribute != other.Attribute || (key.Derived == nil) != (other.Derived == nil) {
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
		ac, bc := key.Class, other.Class
		if ac.Name == "" {
			ac, _ = sqltype.DefaultIndexClass(au.Type)
		}
		if bc.Name == "" {
			bc, _ = sqltype.DefaultIndexClass(bu.Type)
		}
		if ac.Family != bc.Family {
			return false, nil
		}
		if key.Derived != nil {
			left, err := render(key.Derived.Definition)
			if err != nil {
				return false, err
			}
			right, err := render(other.Derived.Definition)
			if err != nil {
				return false, err
			}
			if left != right {
				return false, nil
			}
		}
	}
	if (a.Predicate == nil) != (b.Predicate == nil) {
		return false, nil
	}
	if a.Predicate != nil {
		left, err := render(a.Predicate.Definition)
		if err != nil {
			return false, err
		}
		right, err := render(b.Predicate.Definition)
		if err != nil {
			return false, err
		}
		if left != right {
			return false, nil
		}
	}
	return true, nil
}

func stagePartitionDescriptor(stage *CatalogStage, d *catalog.Relation) error {
	if stage.Base.Get(d.Address()) == nil {
		stage.UpdateDescriptor(d)
		return nil
	}
	return stage.Replace(d)
}

func (e *Engine) stagePartitionIndexChild(op *Operation, stage *CatalogStage, table, parentIndex, child *catalog.Relation, allowIncompleteVirtual bool) (*catalog.Relation, error) {
	view := e.stageRead(stage)
	ctx := aclItemOutputContext(op.Context(), capturedACLItemOutput{owner: e.catalog.owner(), roles: e.roles.read(e.catalog.owner(), view, e.operationRolesLocked(op))})
	if stage.Base.Get(parentIndex.Address()) != nil {
		stage.domainBases = append(stage.domainBases, parentIndex)
	}
	mapped, err := mapPartitionIndex(ctx, view, table, child, parentIndex.SQLIndex)
	if err != nil {
		return nil, err
	}
	mapped.PartitionParent = parentIndex.Address()
	mapped.Valid = true
	mapped.Constraint = parentIndex.SQLIndex.Constraint
	if err = partitionUniqueCoverage(child, mapped); err != nil {
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
		match, err := partitionIndexEquivalent(ctx, view, child, mapped, candidate.SQLIndex)
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
			if err = stagePartitionDescriptor(stage, &changed); err != nil {
				return nil, err
			}
		}
		if err = stagePartitionDescriptor(stage, &next); err != nil {
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
	used := constraintNames(view, child.Namespace(), child)
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
	name := generatedConstraintName(child.Name(), addition, suffix, used)
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
			mapped.Constraint = exclusionBacking(&c)
		} else {
			c := catalog.UniqueConstraint{OID: cid, Name: name, Index: index.Address(), Parent: backing.OID, Primary: backing.Primary, NullsNotDistinct: mapped.NullsNotDistinct, Attributes: make([]int32, len(mapped.Keys))}
			for i, key := range mapped.Keys {
				c.Attributes[i] = key.Attribute
			}
			changed.Constraints.Unique = append(slices.Clone(child.Constraints.Unique), c)
			mapped.Constraint = uniqueBacking(&c)
		}
		if err = stagePartitionDescriptor(stage, &changed); err != nil {
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

func (e *Engine) stagePartitionIndexChildren(op *Operation, stage *CatalogStage, table, index *catalog.Relation, allowIncompleteVirtual bool) error {
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
	valid := partitionIndexCoverageComplete(view, index)
	if index.SQLIndex.Valid != valid {
		next, definition := *index, *index.SQLIndex
		definition.Valid = valid
		next.SQLIndex = &definition
		next.ObjectRevision++
		return stagePartitionDescriptor(stage, &next)
	}
	return nil
}

// Inline inherited indexes exist before the child's local declarations on
// replay. Translate immediate constraint identities in place so neither stored
// local-first slices nor collision-reminted backing OIDs change that order.
func appendPartitionInlineIndexes(view catalog.Read, table *catalog.Relation, indexes []*catalog.Relation) []*catalog.Relation {
	if table.Membership != nil {
		start := len(indexes)
		parent := view.MustRelation(table.Membership.Parent)
		indexes = appendPartitionInlineIndexes(view, parent, indexes)
		end := len(indexes)
		write := start
		for _, inherited := range indexes[start:end] {
			for _, constraint := range table.Constraints.Unique {
				if constraint.Parent == inherited.SQLIndex.Constraint.OID {
					indexes[write] = view.MustRelation(constraint.Index)
					write++
					break
				}
			}
			for _, constraint := range table.Constraints.Exclusion {
				if constraint.Parent == inherited.SQLIndex.Constraint.OID {
					indexes[write] = view.MustRelation(constraint.Index)
					write++
					break
				}
			}
		}
		indexes = indexes[:write]
	}
	for _, constraint := range table.Constraints.Unique {
		index := view.MustRelation(constraint.Index)
		if constraint.Parent == 0 && !usingIndexDump(index) {
			indexes = append(indexes, index)
		}
	}
	for _, constraint := range table.Constraints.Exclusion {
		if constraint.Parent == 0 {
			indexes = append(indexes, view.MustRelation(constraint.Index))
		}
	}
	return indexes
}

// This is the declaration order that creates automatic partition backings:
// inherited inline keys, local inline keys, then separately emitted indexes.
// Only the separate-index tail uses sparse collision provenance / OID ordering.
func partitionIndexDeclarationOrder(view catalog.Read, table *catalog.Relation) []*catalog.Relation {
	indexes := appendPartitionInlineIndexes(view, table, nil)
	inline := len(indexes)
	for _, descriptor := range view.Descriptors() {
		index, ok := descriptor.(*catalog.Relation)
		if !ok || index.SQLIndex == nil || index.SQLIndex.Parent != table.Address() {
			continue
		}
		if backing := index.SQLIndex.Constraint; backing != nil && (backing.Kind == catalog.UniqueBackingKind && !usingIndexDump(index) || backing.Kind == catalog.ExclusionBackingKind) {
			continue
		}
		indexes = append(indexes, index)
	}
	slices.SortFunc(indexes[inline:], func(a, b *catalog.Relation) int {
		priority := func(index *catalog.Relation) ids.OID {
			if source := view.State().DumpPriority(index.Address()); source != 0 {
				return source
			}
			return index.Address().ObjectID
		}
		left, right := priority(a), priority(b)
		if left < right {
			return -1
		}
		if left > right {
			return 1
		}
		if a.Address().ObjectID < b.Address().ObjectID {
			return -1
		}
		if a.Address().ObjectID > b.Address().ObjectID {
			return 1
		}
		return 0
	})
	return indexes
}
