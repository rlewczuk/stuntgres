package engine

import (
	"context"
	"fmt"
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func validatePartitionDescriptor(view catalog.Read, d *catalog.Relation) error {
	invalid := func() error { return fmt.Errorf("stuntgres: invalid partition hierarchy descriptor") }
	if (d.Partition != nil) != (d.RelationKind == catalog.PartitionedRelation) {
		return invalid()
	}
	if d.Membership != nil && d.RelationKind != catalog.TableRelation && d.RelationKind != catalog.PartitionedRelation {
		return invalid()
	}
	if p := d.Partition; p != nil {
		if len(p.Keys) == 0 || len(p.Keys) > 32 || len(d.ColumnMeta.Generations) != 0 || len(d.ColumnMeta.Identities) != 0 {
			return invalid()
		}
		for _, check := range d.Constraints.Check {
			if check.NoInherit {
				return invalid()
			}
		}
		for _, key := range p.Keys {
			if !key.Usage.Valid() || !key.Usage.Type.Concrete() {
				return invalid()
			}
			if err := stored.ValidateUsageCollation(view, key.Usage); err != nil {
				return err
			}
			if key.Collation != 0 && key.Collation != key.Usage.Collation.ID {
				return invalid()
			}
			if key.Derived == nil && !d.HasAttribute(key.Attribute) {
				return invalid()
			}
			if key.Derived != nil && !slices.Equal(key.Derived.Attributes, d.Attributes) {
				return invalid()
			}
			if p.Strategy == parser.PartitionHash {
				hash, err := sqltype.BindPartitionHash(key.Usage, key.Class)
				if err != nil {
					return err
				}
				if hash != key.Hash {
					return invalid()
				}
			} else if p.Strategy == parser.PartitionRange {
				comparison, err := sqltype.BindIndexOrdering(key.Usage, key.Class)
				if err != nil {
					return err
				}
				if comparison != key.Comparison {
					return invalid()
				}
			} else {
				return invalid()
			}
		}
		seen := make(map[ids.ObjectAddress]bool, len(p.Children))
		for i, a := range p.Children {
			child, ok := view.Relation(a)
			if !ok || seen[a] || child.Membership == nil || child.Membership.Parent != d.Address() {
				return invalid()
			}
			seen[a] = true
			if child.Membership.Strategy != p.Strategy {
				return invalid()
			}
			if p.Strategy == parser.PartitionHash {
				if !validHashPartitionBound(child.Membership) || p.DefaultChild != (ids.ObjectAddress{}) {
					return invalid()
				}
				for _, earlier := range p.Children[:i] {
					previous := view.MustRelation(earlier).Membership
					small, large := previous, child.Membership
					if small.Modulus > large.Modulus {
						small, large = large, small
					}
					if large.Modulus%small.Modulus != 0 || hashPartitionOverlap(small, large) {
						return invalid()
					}
				}
				continue
			}
			if child.Membership.DefaultBound != (p.DefaultChild == a) {
				return invalid()
			}
			if !child.Membership.DefaultBound {
				if len(child.Membership.Lower) != len(p.Keys) || len(child.Membership.Upper) != len(p.Keys) {
					return invalid()
				}
				for _, earlier := range p.Children[:i] {
					previous := view.MustRelation(earlier).Membership
					if previous.DefaultBound {
						continue
					}
					left, _, err := comparePartitionTuple(context.Background(), p.Keys, previous.Lower, child.Membership.Upper, sqltype.InputEnvironment{})
					if err != nil {
						return err
					}
					if left >= 0 {
						continue
					}
					right, _, err := comparePartitionTuple(context.Background(), p.Keys, child.Membership.Lower, previous.Upper, sqltype.InputEnvironment{})
					if err != nil {
						return err
					}
					if right < 0 {
						return invalid()
					}
				}
			}
		}
		if p.DefaultChild != (ids.ObjectAddress{}) && !seen[p.DefaultChild] {
			return invalid()
		}
	}
	if membership := d.Membership; membership != nil {
		parent, ok := view.Relation(membership.Parent)
		if !ok || parent.Partition == nil || !slices.Contains(parent.Partition.Children, d.Address()) || len(parent.Columns) != len(d.Columns) {
			return invalid()
		}
		parentNamespace, parentExists := view.Namespace(parent.Namespace())
		childNamespace, childExists := view.Namespace(d.Namespace())
		if !parentExists || !childExists || parentNamespace.Temporary != childNamespace.Temporary {
			return invalid()
		}
		for _, c := range parent.Columns {
			slot, exists := d.Index[c.Name]
			if !exists || !catalog.SamePartitionColumnUsage(c.Usage, d.Columns[slot].Usage) || !c.Nullable && d.Columns[slot].Nullable {
				return invalid()
			}
		}
		if membership.Strategy != parent.Partition.Strategy {
			return invalid()
		}
		if membership.Strategy == parser.PartitionHash {
			if !validHashPartitionBound(membership) {
				return invalid()
			}
		} else if !membership.DefaultBound {
			if len(membership.Lower) != len(parent.Partition.Keys) || len(membership.Upper) != len(parent.Partition.Keys) {
				return invalid()
			}
			for _, tuple := range [2][]catalog.PartitionDatum{membership.Lower, membership.Upper} {
				var sentinel int8
				for i, key := range parent.Partition.Keys {
					v := tuple[i]
					if v.Kind < -1 || v.Kind > 1 || sentinel != 0 && v.Kind != sentinel || v.Kind == 0 && (v.Value.IsNull() || v.Value.Type() != key.Usage.Type) {
						return invalid()
					}
					if v.Kind != 0 {
						sentinel = v.Kind
					}
				}
			}
			order, _, err := comparePartitionTuple(context.Background(), parent.Partition.Keys, membership.Lower, membership.Upper, sqltype.InputEnvironment{})
			if err != nil {
				return err
			}
			if order >= 0 {
				return invalid()
			}
		} else if len(membership.Lower) != 0 || len(membership.Upper) != 0 {
			return invalid()
		}
		visited := map[ids.ObjectAddress]bool{d.Address(): true}
		for ancestor := parent; ancestor != nil; {
			if visited[ancestor.Address()] {
				return invalid()
			}
			visited[ancestor.Address()] = true
			if ancestor.Membership == nil {
				break
			}
			ancestor, ok = view.Relation(ancestor.Membership.Parent)
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
func comparePartitionTuple(ctx context.Context, keys []catalog.PartitionKeyEntry, a, b []catalog.PartitionDatum, input sqltype.InputEnvironment) (int, int, error) {
	for i, key := range keys {
		if err := ctx.Err(); err != nil {
			return 0, i, err
		}
		if a[i].Kind != b[i].Kind {
			if a[i].Kind < b[i].Kind {
				return -1, i, nil
			}
			return 1, i, nil
		}
		if a[i].Kind != 0 {
			return 0, i, nil
		}
		order, err := key.Comparison.Compare(ctx, a[i].Value, b[i].Value, input)
		if err != nil || order != 0 {
			return order, i, err
		}
	}
	return 0, len(keys) - 1, nil
}

type partitionEvaluatedKey struct {
	values  []catalog.PartitionDatum
	anyNull bool
	hash    uint64
}

func partitionRowKey(d *catalog.Relation, row []sqltype.Value, evaluation *exec.Context, buffer []catalog.PartitionDatum) (partitionEvaluatedKey, error) {
	keys := d.Partition.Keys
	result := partitionEvaluatedKey{values: buffer[:len(keys)]}
	for i, key := range keys {
		if err := evaluation.Context.Err(); err != nil {
			return partitionEvaluatedKey{}, err
		}
		var value sqltype.Value
		var err error
		if key.Derived == nil {
			value = row[slices.Index(d.Attributes, key.Attribute)]
		} else {
			value, err = evaluatePartitionRecipe(evaluation, d, i, row)
			if err != nil {
				return partitionEvaluatedKey{}, err
			}
		}
		result.values[i] = catalog.PartitionDatum{Value: value}
		result.anyNull = result.anyNull || value.IsNull()
		if d.Partition.Strategy == parser.PartitionHash && !value.IsNull() {
			hashed, err := key.Hash.Hash(evaluation.Context, value, 0x7a5b22367996dcfd)
			if err != nil {
				return partitionEvaluatedKey{}, err
			}
			result.hash ^= hashed + 0x49a0f4dd15e5a8e3 + (result.hash << 54) + (result.hash >> 7)
		}
	}
	return result, nil
}

func partitionRangeContains(ctx context.Context, keys []catalog.PartitionKeyEntry, bound *catalog.PartitionMembership, values []catalog.PartitionDatum, anyNull bool, input sqltype.InputEnvironment) (bool, error) {
	if anyNull {
		return false, nil
	}
	lower, _, err := comparePartitionTuple(ctx, keys, values, bound.Lower, input)
	if err != nil || lower < 0 {
		return false, err
	}
	upper, _, err := comparePartitionTuple(ctx, keys, values, bound.Upper, input)
	return upper < 0, err
}

func partitionContains(ctx context.Context, keys []catalog.PartitionKeyEntry, bound *catalog.PartitionMembership, key partitionEvaluatedKey, input sqltype.InputEnvironment) (bool, error) {
	if bound.Strategy == parser.PartitionHash {
		return key.hash%uint64(bound.Modulus) == uint64(bound.Remainder), ctx.Err()
	}
	return partitionRangeContains(ctx, keys, bound, key.values, key.anyNull, input)
}

// DEFAULT is always the live complement; no copied DEFAULT predicate can
// outlive an explicit sibling's publication or its atomic removal.
func partitionAccepts(view catalog.Read, parent *catalog.Relation, child *catalog.Relation, row []sqltype.Value, evaluation *exec.Context) (bool, error) {
	var buffer [32]catalog.PartitionDatum
	key, err := partitionRowKey(parent, row, evaluation, buffer[:])
	if err != nil {
		return false, err
	}
	if !child.Membership.DefaultBound {
		return partitionContains(evaluation.Context, parent.Partition.Keys, child.Membership, key, evaluation.Input)
	}
	for _, a := range parent.Partition.Children {
		sibling := view.MustRelation(a)
		if sibling.Membership.DefaultBound {
			continue
		}
		contains, err := partitionContains(evaluation.Context, parent.Partition.Keys, sibling.Membership, key, evaluation.Input)
		if err != nil || contains {
			return false, err
		}
	}
	return true, nil
}

func partitionPhysicalDescendants(catalog catalog.Read, d *catalog.Relation, visit func(*catalog.Relation) error) error {
	if d.Partition == nil {
		return visit(d)
	}
	for _, a := range d.Partition.Children {
		child, ok := catalog.Relation(a)
		if !ok {
			return ids.ErrCatalogReplaced
		}
		if err := partitionPhysicalDescendants(catalog, child, visit); err != nil {
			return err
		}
	}
	return nil
}
