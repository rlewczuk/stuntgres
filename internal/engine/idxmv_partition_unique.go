package engine

import (
	"context"
	"fmt"
	"slices"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func PartitionUniqueCoverage(table *catalog.Relation, index *catalog.IndexDefinition) error {
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
		d.Exclusion, err = RebindExclusionOperators(child, &d)
		if err != nil {
			return nil, err
		}
	}
	return &d, nil
}

func PartitionIndexEquivalent(ctx context.Context, view catalog.Read, table *catalog.Relation, a, b *catalog.IndexDefinition) (bool, error) {
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
	render := func(expr expr.Expr) (string, error) {
		return (stored.ScalarRenderer{Owner: table}).SQL(ctx, view, expr)
	}
	for i, key := range a.Keys {
		other := b.Keys[i]
		if key.Attribute != other.Attribute || (key.Derived == nil) != (other.Derived == nil) {
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

func StagePartitionDescriptor(stage indexDescriptorStage, d *catalog.Relation) error {
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
