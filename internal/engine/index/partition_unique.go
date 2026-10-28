package index

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqlstate"
)

func PartitionUniqueCoverage(view catalog.Read, table *catalog.Relation, index *catalog.IndexDefinition) error {
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
			usage := KeyCollation(table.Columns[table.IndexForAttribute(key.Attribute)].Usage, key)
			if index.IsExclusion() {
				if usage.Collation != partitionKey.Usage.Collation {
					continue
				}
				operator := index.Exclusion[i]
				if !operator.IsEquality() {
					return sqlError("0A000", fmt.Sprintf(`cannot match partition key to index on column "%s" using non-equal operator "%s"`, partitionKey.Label, operator.Name))
				}
				inputs, err := operator.OperatorInputs()
				equality, bindErr := routine.BindExclusionOperator(view, partitionKey.Usage, partitionKey.Class, "=")
				expected, inputErr := equality.OperatorInputs()
				found = err == nil && bindErr == nil && inputErr == nil && operator.Name == equality.Name && inputs == expected
				if found {
					break
				}
				continue
			}
			class := key.Class
			if class.Name == "" {
				var err error
				class, _, err = view.DefaultIndexClass(index.MethodIdentity(), usage.Type)
				if err != nil {
					return err
				}
			}
			selected, err := routine.BindClassSupport(view, class)
			partitionSelected, partitionErr := routine.BindClassSupport(view, partitionKey.Class)
			if err != nil || partitionErr != nil || !selected.Accepts(usage.Type) {
				continue
			}
			left, right := selected.Support(1), partitionSelected.Support(1)
			partitionStrategy := int16(3)
			if partitionSelected.IsHash() {
				partitionStrategy = 1
			}
			equality, exists := view.FamilyOperator(ids.OID(left.Family), ids.OID(left.Left), ids.OID(left.Right), 3, 's')
			partitionEquality, partitionExists := view.FamilyOperator(ids.OID(right.Family), ids.OID(right.Left), ids.OID(right.Right), partitionStrategy, 's')
			found = exists && partitionExists && usage.Collation == partitionKey.Usage.Collation && equality.Operator == partitionEquality.Operator && equality.Left == partitionEquality.Left && equality.Right == partitionEquality.Right
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
func MapPartitionIndex(ctx context.Context, view catalog.Read, source, child *catalog.Relation, old *catalog.IndexDefinition) (*catalog.IndexDefinition, error) {
	in := analyze.LikeInput{Source: source, TargetSlots: make([]int, len(source.Columns))}
	for i, column := range source.Columns {
		slot, ok := child.Index[column.Name]
		if !ok {
			return nil, fmt.Errorf("stuntgres: missing partition index column")
		}
		in.TargetSlots[i] = slot
	}
	d := *old
	d.Parent, d.PartitionParent, d.Constraint = child.Address(), ids.ObjectAddress{}, nil
	d.Keys, d.Include = slices.Clone(old.Keys), slices.Clone(old.Include)
	for i := range d.Include {
		a, err := in.Attribute(child, d.Include[i])
		if err != nil {
			return nil, err
		}
		d.Include[i] = a
	}
	for i := range d.Keys {
		key := &d.Keys[i]
		if key.Derived == nil {
			a, err := in.Attribute(child, key.Attribute)
			if err != nil {
				return nil, err
			}
			key.Attribute = a
			continue
		}
		node, err := stored.RemapLikeExpression(&in, key.Derived.Definition, child, view)
		if err != nil {
			return nil, err
		}
		execution, err := stored.RemapLikeExpression(&in, key.Derived.Execution, child, view)
		if err != nil {
			return nil, err
		}
		derived := &catalog.DerivedIndexKey{Definition: node, Execution: execution, Attributes: child.Attributes}
		if _, err = (stored.ScalarRenderer{Validation: true, Owner: child, Dependencies: &derived.Dependencies}).SQL(ctx, view, node); err != nil {
			return nil, err
		}
		key.Derived = derived
	}
	if old.Predicate != nil {
		node, err := stored.RemapLikeExpression(&in, old.Predicate.Definition, child, view)
		if err != nil {
			return nil, err
		}
		execution, err := stored.RemapLikeExpression(&in, old.Predicate.Execution, child, view)
		if err != nil {
			return nil, err
		}
		predicate := &catalog.IndexPredicate{Definition: node, Execution: execution, Attributes: child.Attributes}
		if _, err = (stored.ScalarRenderer{Validation: true, Owner: child, Dependencies: &predicate.Dependencies}).SQL(ctx, view, node); err != nil {
			return nil, err
		}
		d.Predicate = predicate
	}
	if old.IsExclusion() {
		var err error
		d.Exclusion, err = RebindExclusionOperators(view, child, &d)
		if err != nil {
			return nil, err
		}
	}
	return &d, nil
}

func PartitionIndexEquivalent(ctx context.Context, view catalog.Read, table *catalog.Relation, a, b *catalog.IndexDefinition) (bool, error) {
	if a.MethodIdentity() != b.MethodIdentity() || a.Method != b.Method || a.Unique != b.Unique || a.NullsNotDistinct != b.NullsNotDistinct || len(a.Keys) != len(b.Keys) || !slices.Equal(a.Include, b.Include) {
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
	render := func(expr expr.Expr) (string, error) {
		return (stored.ScalarRenderer{Owner: table}).SQL(ctx, view, expr)
	}
	for i, key := range a.Keys {
		other := b.Keys[i]
		if key.Attribute != other.Attribute || (key.Derived == nil) != (other.Derived == nil) || key.ClassOptions != other.ClassOptions {
			return false, nil
		}
		au, err := KeyUsage(table, key)
		if err != nil {
			return false, err
		}
		bu, err := KeyUsage(table, other)
		if err != nil {
			return false, err
		}
		if KeyCollation(au, key).Collation != KeyCollation(bu, other).Collation {
			return false, nil
		}
		ac, bc := key.Class, other.Class
		if ac.Name == "" {
			ac, _, err = view.DefaultIndexClass(a.MethodIdentity(), au.Type)
			if err != nil {
				return false, err
			}
		}
		if bc.Name == "" {
			bc, _, err = view.DefaultIndexClass(b.MethodIdentity(), bu.Type)
			if err != nil {
				return false, err
			}
		}
		if ac.MethodOID() != bc.MethodOID() || ac.FamilyOID() != bc.FamilyOID() {
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

func StagePartitionDescriptor(stage DescriptorStage, d *catalog.Relation) error {
	if stage.StageBase().Get(d.Address()) == nil {
		stage.UpdateDescriptor(d)
		return nil
	}
	return stage.Replace(d)
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
		if constraint.Parent == 0 && !UsingIndexDump(index) {
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
func PartitionIndexDeclarationOrder(view catalog.Read, table *catalog.Relation) []*catalog.Relation {
	indexes := appendPartitionInlineIndexes(view, table, nil)
	inline := len(indexes)
	for _, descriptor := range view.Descriptors() {
		index, ok := descriptor.(*catalog.Relation)
		if !ok || index.SQLIndex == nil || index.SQLIndex.Parent != table.Address() {
			continue
		}
		if backing := index.SQLIndex.Constraint; backing != nil && (backing.Kind == catalog.UniqueBackingKind && !UsingIndexDump(index) || backing.Kind == catalog.ExclusionBackingKind) {
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

// StagePartitionIndexChild attaches or creates the index of partition child
// that corresponds to parentIndex of table, with its backing constraint, and
// recurses into a partitioned child.
func StagePartitionIndexChild(h Host, table, parentIndex, child *catalog.Relation, allowIncompleteVirtual bool) (*catalog.Relation, error) {
	view := h.StageRead()
	ctx := regobj.ACLItemOutputContext(h.Context(), h.ACLOutput(view))
	if h.StageBase().Get(parentIndex.Address()) != nil {
		h.AddDomainBase(parentIndex)
	}
	mapped, err := MapPartitionIndex(ctx, view, table, child, parentIndex.SQLIndex)
	if err != nil {
		return nil, err
	}
	mapped.PartitionParent = parentIndex.Address()
	mapped.Valid = true
	mapped.Constraint = parentIndex.SQLIndex.Constraint
	if err = PartitionUniqueCoverage(view, child, mapped); err != nil {
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
		if parentKey := UniqueDefinition(table, parentIndex); parentKey != nil {
			childKey := UniqueDefinition(child, candidate)
			if childKey == nil || childKey.Deferrable != parentKey.Deferrable || childKey.InitiallyDeferred != parentKey.InitiallyDeferred {
				continue
			}
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
			if err = StagePartitionDescriptor(h.DescriptorStage(), &changed); err != nil {
				return nil, err
			}
		}
		if err = StagePartitionDescriptor(h.DescriptorStage(), &next); err != nil {
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
	idx := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid}, ObjectNamespace: child.Namespace(), ObjectName: name, ObjectRevision: revision, Owner: child.Owner, RelationKind: catalog.IndexRelation, SQLIndex: mapped, Columns: slices.Clone(parentIndex.Columns), Attributes: slices.Clone(parentIndex.Attributes), ColumnMeta: catalog.ColumnMetadata{HighWater: parentIndex.ColumnMeta.HighWater}, Index: make(map[string]int, len(parentIndex.Columns)), StorageOptions: parentIndex.StorageOptions}
	if err := CopyColumnStorage(view, child, idx); err != nil { return nil, err }
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
			mapped.Constraint = ExclusionBacking(&c)
		} else {
			c := catalog.UniqueConstraint{OID: cid, Name: name, Index: idx.Address(), Parent: backing.OID, Primary: backing.Primary, NullsNotDistinct: mapped.NullsNotDistinct, Attributes: make([]int32, len(mapped.Keys))}
			source := UniqueDefinition(table, parentIndex)
			if source == nil {
				return nil, fmt.Errorf("stuntgres: missing partition unique definition")
			}
			c.Deferrable, c.InitiallyDeferred = source.Deferrable, source.InitiallyDeferred
			for i, key := range mapped.Keys {
				c.Attributes[i] = key.Attribute
			}
			changed.Constraints.Unique = append(slices.Clone(child.Constraints.Unique), c)
			mapped.Constraint = UniqueBacking(&c)
		}
		if err = StagePartitionDescriptor(h.DescriptorStage(), &changed); err != nil {
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
func StagePartitionIndexChildren(h Host, table, idx *catalog.Relation, allowIncompleteVirtual bool) error {
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
	valid := PartitionIndexCoverageComplete(view, idx)
	if idx.SQLIndex.Valid != valid {
		next, definition := *idx, *idx.SQLIndex
		definition.Valid = valid
		next.SQLIndex = &definition
		next.ObjectRevision++
		return StagePartitionDescriptor(h.DescriptorStage(), &next)
	}
	return nil
}
