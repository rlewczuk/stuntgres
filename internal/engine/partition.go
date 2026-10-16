package engine

import (
	"context"
	"fmt"
	"slices"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type partitionKeyEntry struct {
	attribute     int32
	derived       *derivedIndexKey
	usage         sqltype.Usage
	class         sqltype.IndexClass
	comparison    sqltype.IndexComparison
	hash          sqltype.PartitionHash
	collation     uint32
	explicitClass bool
	label         string
}
type partitionDefinition struct {
	strategy     parser.PartitionStrategy
	keys         []partitionKeyEntry
	children     []ObjectAddress
	defaultChild ObjectAddress
}
type partitionDatum struct {
	kind  int8
	value sqltype.Value
	pos   parser.Position
}
type partitionMembership struct {
	parent             ObjectAddress
	strategy           parser.PartitionStrategy
	modulus, remainder int32
	defaultBound       bool
	lower, upper       []partitionDatum
}

func hierarchyRelation(d *relationDescriptor) bool { return d.partition != nil || d.membership != nil }

func validatePartitionDescriptor(catalog CatalogRead, d *relationDescriptor) error {
	invalid := func() error { return fmt.Errorf("stuntgres: invalid partition hierarchy descriptor") }
	if (d.partition != nil) != (d.relationKind == partitionedRelation) {
		return invalid()
	}
	if d.membership != nil && d.relationKind != tableRelation && d.relationKind != partitionedRelation {
		return invalid()
	}
	if p := d.partition; p != nil {
		if len(p.keys) == 0 || len(p.keys) > 32 || len(d.generations) != 0 || len(d.identities) != 0 {
			return invalid()
		}
		for _, check := range d.checkConstraints {
			if check.NoInherit {
				return invalid()
			}
		}
		for _, key := range p.keys {
			if !key.usage.Valid() || !key.usage.Type.Concrete() {
				return invalid()
			}
			if err := validateUsageCollation(catalog, key.usage); err != nil {
				return err
			}
			if key.collation != 0 && key.collation != key.usage.Collation.ID {
				return invalid()
			}
			if key.derived == nil && !d.hasAttribute(key.attribute) {
				return invalid()
			}
			if key.derived != nil && !slices.Equal(key.derived.attributes, d.attributes) {
				return invalid()
			}
			if p.strategy == parser.PartitionHash {
				hash, err := sqltype.BindPartitionHash(key.usage, key.class)
				if err != nil {
					return err
				}
				if hash != key.hash {
					return invalid()
				}
			} else if p.strategy == parser.PartitionRange {
				comparison, err := sqltype.BindIndexOrdering(key.usage, key.class)
				if err != nil {
					return err
				}
				if comparison != key.comparison {
					return invalid()
				}
			} else {
				return invalid()
			}
		}
		seen := make(map[ObjectAddress]bool, len(p.children))
		for i, a := range p.children {
			child, ok := catalog.state.descriptors[a].(*relationDescriptor)
			if !ok || seen[a] || child.membership == nil || child.membership.parent != d.address {
				return invalid()
			}
			seen[a] = true
			if child.membership.strategy != p.strategy {
				return invalid()
			}
			if p.strategy == parser.PartitionHash {
				if !validHashPartitionBound(child.membership) || p.defaultChild != (ObjectAddress{}) {
					return invalid()
				}
				for _, earlier := range p.children[:i] {
					previous := catalog.state.descriptors[earlier].(*relationDescriptor).membership
					small, large := previous, child.membership
					if small.modulus > large.modulus {
						small, large = large, small
					}
					if large.modulus%small.modulus != 0 || hashPartitionOverlap(small, large) {
						return invalid()
					}
				}
				continue
			}
			if child.membership.defaultBound != (p.defaultChild == a) {
				return invalid()
			}
			if !child.membership.defaultBound {
				if len(child.membership.lower) != len(p.keys) || len(child.membership.upper) != len(p.keys) {
					return invalid()
				}
				for _, earlier := range p.children[:i] {
					previous := catalog.state.descriptors[earlier].(*relationDescriptor).membership
					if previous.defaultBound {
						continue
					}
					left, _, err := comparePartitionTuple(context.Background(), p.keys, previous.lower, child.membership.upper, sqltype.InputEnvironment{})
					if err != nil {
						return err
					}
					if left >= 0 {
						continue
					}
					right, _, err := comparePartitionTuple(context.Background(), p.keys, child.membership.lower, previous.upper, sqltype.InputEnvironment{})
					if err != nil {
						return err
					}
					if right < 0 {
						return invalid()
					}
				}
			}
		}
		if p.defaultChild != (ObjectAddress{}) && !seen[p.defaultChild] {
			return invalid()
		}
	}
	if membership := d.membership; membership != nil {
		parent, ok := catalog.state.descriptors[membership.parent].(*relationDescriptor)
		if !ok || parent.partition == nil || !slices.Contains(parent.partition.children, d.address) || len(parent.columns) != len(d.columns) {
			return invalid()
		}
		parentNamespace, parentExists := catalog.state.descriptors[parent.namespace].(*namespaceDescriptor)
		childNamespace, childExists := catalog.state.descriptors[d.namespace].(*namespaceDescriptor)
		if !parentExists || !childExists || parentNamespace.temporary != childNamespace.temporary {
			return invalid()
		}
		for _, c := range parent.columns {
			slot, exists := d.index[c.Name]
			if !exists || !samePartitionColumnUsage(c.Usage, d.columns[slot].Usage) || !c.Nullable && d.columns[slot].Nullable {
				return invalid()
			}
		}
		if membership.strategy != parent.partition.strategy {
			return invalid()
		}
		if membership.strategy == parser.PartitionHash {
			if !validHashPartitionBound(membership) {
				return invalid()
			}
		} else if !membership.defaultBound {
			if len(membership.lower) != len(parent.partition.keys) || len(membership.upper) != len(parent.partition.keys) {
				return invalid()
			}
			for _, tuple := range [2][]partitionDatum{membership.lower, membership.upper} {
				var sentinel int8
				for i, key := range parent.partition.keys {
					v := tuple[i]
					if v.kind < -1 || v.kind > 1 || sentinel != 0 && v.kind != sentinel || v.kind == 0 && (v.value.IsNull() || v.value.Type() != key.usage.Type) {
						return invalid()
					}
					if v.kind != 0 {
						sentinel = v.kind
					}
				}
			}
			order, _, err := comparePartitionTuple(context.Background(), parent.partition.keys, membership.lower, membership.upper, sqltype.InputEnvironment{})
			if err != nil {
				return err
			}
			if order >= 0 {
				return invalid()
			}
		} else if len(membership.lower) != 0 || len(membership.upper) != 0 {
			return invalid()
		}
		visited := map[ObjectAddress]bool{d.address: true}
		for ancestor := parent; ancestor != nil; {
			if visited[ancestor.address] {
				return invalid()
			}
			visited[ancestor.address] = true
			if ancestor.membership == nil {
				break
			}
			ancestor, ok = catalog.state.descriptors[ancestor.membership.parent].(*relationDescriptor)
			if !ok {
				return invalid()
			}
		}
	}
	return nil
}

// The second result is the deciding component, including ties: equal infinite
// datums terminate comparison there; equal finite tuples terminate at the last
// component. Callers needing PostgreSQL's equal-lower overlap position explicitly
// select the first component instead.
func comparePartitionTuple(ctx context.Context, keys []partitionKeyEntry, a, b []partitionDatum, input sqltype.InputEnvironment) (int, int, error) {
	for i, key := range keys {
		if err := ctx.Err(); err != nil {
			return 0, i, err
		}
		if a[i].kind != b[i].kind {
			if a[i].kind < b[i].kind {
				return -1, i, nil
			}
			return 1, i, nil
		}
		if a[i].kind != 0 {
			return 0, i, nil
		}
		order, err := key.comparison.Compare(ctx, a[i].value, b[i].value, input)
		if err != nil || order != 0 {
			return order, i, err
		}
	}
	return 0, len(keys) - 1, nil
}

type partitionEvaluatedKey struct {
	values  []partitionDatum
	anyNull bool
	hash    uint64
}

func partitionRowKey(d *relationDescriptor, row []sqltype.Value, evaluation *EvalContext, buffer []partitionDatum) (partitionEvaluatedKey, error) {
	keys := d.partition.keys
	result := partitionEvaluatedKey{values: buffer[:len(keys)]}
	for i, key := range keys {
		if err := evaluation.Context.Err(); err != nil {
			return partitionEvaluatedKey{}, err
		}
		var value sqltype.Value
		var err error
		if key.derived == nil {
			value = row[slices.Index(d.attributes, key.attribute)]
		} else {
			value, err = evaluatePartitionRecipe(evaluation, d, i, row)
			if err != nil {
				return partitionEvaluatedKey{}, err
			}
		}
		result.values[i] = partitionDatum{value: value}
		result.anyNull = result.anyNull || value.IsNull()
		if d.partition.strategy == parser.PartitionHash && !value.IsNull() {
			hashed, err := key.hash.Hash(evaluation.Context, value, 0x7a5b22367996dcfd)
			if err != nil {
				return partitionEvaluatedKey{}, err
			}
			result.hash ^= hashed + 0x49a0f4dd15e5a8e3 + (result.hash << 54) + (result.hash >> 7)
		}
	}
	return result, nil
}

func partitionRangeContains(ctx context.Context, keys []partitionKeyEntry, bound *partitionMembership, values []partitionDatum, anyNull bool, input sqltype.InputEnvironment) (bool, error) {
	if anyNull {
		return false, nil
	}
	lower, _, err := comparePartitionTuple(ctx, keys, values, bound.lower, input)
	if err != nil || lower < 0 {
		return false, err
	}
	upper, _, err := comparePartitionTuple(ctx, keys, values, bound.upper, input)
	return upper < 0, err
}

func partitionContains(ctx context.Context, keys []partitionKeyEntry, bound *partitionMembership, key partitionEvaluatedKey, input sqltype.InputEnvironment) (bool, error) {
	if bound.strategy == parser.PartitionHash {
		return key.hash%uint64(bound.modulus) == uint64(bound.remainder), ctx.Err()
	}
	return partitionRangeContains(ctx, keys, bound, key.values, key.anyNull, input)
}

// DEFAULT is always the live complement; no copied DEFAULT predicate can
// outlive an explicit sibling's publication or its atomic removal.
func partitionAccepts(catalog CatalogRead, parent *relationDescriptor, child *relationDescriptor, row []sqltype.Value, evaluation *EvalContext) (bool, error) {
	var buffer [32]partitionDatum
	key, err := partitionRowKey(parent, row, evaluation, buffer[:])
	if err != nil {
		return false, err
	}
	if !child.membership.defaultBound {
		return partitionContains(evaluation.Context, parent.partition.keys, child.membership, key, evaluation.Input)
	}
	for _, a := range parent.partition.children {
		sibling := catalog.state.descriptors[a].(*relationDescriptor)
		if sibling.membership.defaultBound {
			continue
		}
		contains, err := partitionContains(evaluation.Context, parent.partition.keys, sibling.membership, key, evaluation.Input)
		if err != nil || contains {
			return false, err
		}
	}
	return true, nil
}

func partitionPhysicalDescendants(catalog CatalogRead, d *relationDescriptor, visit func(*relationDescriptor) error) error {
	if d.partition == nil {
		return visit(d)
	}
	for _, a := range d.partition.children {
		child, ok := catalog.state.descriptors[a].(*relationDescriptor)
		if !ok {
			return ErrCatalogReplaced
		}
		if err := partitionPhysicalDescendants(catalog, child, visit); err != nil {
			return err
		}
	}
	return nil
}
