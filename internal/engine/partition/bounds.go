package partition

import (
	"context"
	"fmt"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func ValidateDescriptor(view catalog.Read, d *catalog.Relation) error {
	invalid := func() error { return fmt.Errorf("stuntgres: invalid partition hierarchy descriptor") }
	if err := ValidateConstraintOrigins(view, d); err != nil {
		return err
	}
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
		if p.Strategy == parser.PartitionList && len(p.Keys) != 1 {
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
				hash, err := routine.BindPartitionHash(view, key.Usage, key.Class)
				if err != nil {
					return err
				}
				if !hash.SameOperation(key.Hash) {
					return invalid()
				}
			} else if p.Strategy == parser.PartitionRange || p.Strategy == parser.PartitionList {
				comparison, err := routine.BindIndexOrdering(view, key.Usage, key.Class)
				if err != nil {
					return err
				}
				if !comparison.SameOperation(key.Comparison) {
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
				if !ValidHashBound(child.Membership) || p.DefaultChild != (ids.ObjectAddress{}) {
					return invalid()
				}
				for _, earlier := range p.Children[:i] {
					previous := view.MustRelation(earlier).Membership
					small, large := previous, child.Membership
					if small.Modulus > large.Modulus {
						small, large = large, small
					}
					if large.Modulus%small.Modulus != 0 || HashOverlap(small, large) {
						return invalid()
					}
				}
				continue
			}
			if child.Membership.DefaultBound != (p.DefaultChild == a) {
				return invalid()
			}
			if p.Strategy == parser.PartitionList {
				if err := validateListBound(context.Background(), &p.Keys[0], child.Membership); err != nil {
					return err
				}
				if !child.Membership.DefaultBound {
					for _, earlier := range p.Children[:i] {
						previous := view.MustRelation(earlier).Membership
						for _, datum := range previous.Values {
							overlap, err := listContains(context.Background(), &p.Keys[0], child.Membership.Values, datum.Value, sqltype.InputEnvironment{})
							if err != nil {
								return err
							}
							if overlap {
								return invalid()
							}
						}
					}
				}
				continue
			}
			if len(child.Membership.Values) != 0 {
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
					left, _, err := CompareTuple(context.Background(), p.Keys, previous.Lower, child.Membership.Upper, sqltype.InputEnvironment{})
					if err != nil {
						return err
					}
					if left >= 0 {
						continue
					}
					right, _, err := CompareTuple(context.Background(), p.Keys, child.Membership.Lower, previous.Upper, sqltype.InputEnvironment{})
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
		if membership.Strategy != parser.PartitionList && len(membership.Values) != 0 {
			return invalid()
		}
		if membership.Strategy == parser.PartitionHash {
			if !ValidHashBound(membership) {
				return invalid()
			}
		} else if membership.Strategy == parser.PartitionList {
			if len(parent.Partition.Keys) != 1 {
				return invalid()
			}
			if err := validateListBound(context.Background(), &parent.Partition.Keys[0], membership); err != nil {
				return err
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
			order, _, err := CompareTuple(context.Background(), parent.Partition.Keys, membership.Lower, membership.Upper, sqltype.InputEnvironment{})
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
func CompareTuple(ctx context.Context, keys []catalog.PartitionKeyEntry, a, b []catalog.PartitionDatum, input sqltype.InputEnvironment) (int, int, error) {
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

type EvaluatedKey struct {
	Values  []catalog.PartitionDatum
	anyNull bool
	Hash    uint64
}

// AccumulateHash is the single released seeded scalar-key combination used by
// both row routing and equality/NULL pruning. NULL contributes no hash datum.
func AccumulateHash(ctx context.Context, key catalog.PartitionKeyEntry, value sqltype.Value, hash uint64) (uint64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if value.IsNull() {
		return hash, nil
	}
	hashed, err := key.Hash.Hash(ctx, value, 0x7a5b22367996dcfd)
	if err != nil {
		return 0, err
	}
	return hash ^ (hashed + 0x49a0f4dd15e5a8e3 + (hash << 54) + (hash >> 7)), nil
}

func RowKey(d *catalog.Relation, row []sqltype.Value, evaluation *exec.Context, buffer []catalog.PartitionDatum) (EvaluatedKey, error) {
	keys := d.Partition.Keys
	result := EvaluatedKey{Values: buffer[:len(keys)]}
	for i, key := range keys {
		if err := evaluation.Context.Err(); err != nil {
			return EvaluatedKey{}, err
		}
		var value sqltype.Value
		var err error
		if key.Derived == nil {
			value = row[slices.Index(d.Attributes, key.Attribute)]
		} else {
			value, err = EvaluateRecipe(evaluation, d, i, row)
			if err != nil {
				return EvaluatedKey{}, err
			}
		}
		result.Values[i] = catalog.PartitionDatum{Value: value}
		result.anyNull = result.anyNull || value.IsNull()
		if d.Partition.Strategy == parser.PartitionHash {
			result.Hash, err = AccumulateHash(evaluation.Context, key, value, result.Hash)
			if err != nil {
				return EvaluatedKey{}, err
			}
		}
	}
	return result, nil
}

func RangeContains(ctx context.Context, keys []catalog.PartitionKeyEntry, bound *catalog.PartitionMembership, values []catalog.PartitionDatum, anyNull bool, input sqltype.InputEnvironment) (bool, error) {
	if anyNull {
		return false, nil
	}
	lower, _, err := CompareTuple(ctx, keys, values, bound.Lower, input)
	if err != nil || lower < 0 {
		return false, err
	}
	upper, _, err := CompareTuple(ctx, keys, values, bound.Upper, input)
	return upper < 0, err
}

func Contains(ctx context.Context, keys []catalog.PartitionKeyEntry, bound *catalog.PartitionMembership, key EvaluatedKey, input sqltype.InputEnvironment) (bool, error) {
	if bound.Strategy == parser.PartitionHash {
		return key.Hash%uint64(bound.Modulus) == uint64(bound.Remainder), ctx.Err()
	}
	if bound.Strategy == parser.PartitionList {
		return listContains(ctx, &keys[0], bound.Values, key.Values[0].Value, input)
	}
	return RangeContains(ctx, keys, bound, key.Values, key.anyNull, input)
}

// DEFAULT is always the live complement; no copied DEFAULT predicate can
// outlive an explicit sibling's publication or its atomic removal.
func Accepts(view catalog.Read, parent *catalog.Relation, child *catalog.Relation, row []sqltype.Value, evaluation *exec.Context) (bool, error) {
	var buffer [32]catalog.PartitionDatum
	key, err := RowKey(parent, row, evaluation, buffer[:])
	if err != nil {
		return false, err
	}
	if !child.Membership.DefaultBound {
		return Contains(evaluation.Context, parent.Partition.Keys, child.Membership, key, evaluation.Input)
	}
	for _, a := range parent.Partition.Children {
		sibling := view.MustRelation(a)
		if sibling.Membership.DefaultBound {
			continue
		}
		contains, err := Contains(evaluation.Context, parent.Partition.Keys, sibling.Membership, key, evaluation.Input)
		if err != nil || contains {
			return false, err
		}
	}
	return true, nil
}

func PhysicalDescendants(catalog catalog.Read, d *catalog.Relation, visit func(*catalog.Relation) error) error {
	if d.Partition == nil {
		return visit(d)
	}
	for _, a := range d.Partition.Children {
		child, ok := catalog.Relation(a)
		if !ok {
			return ids.ErrCatalogReplaced
		}
		if err := PhysicalDescendants(catalog, child, visit); err != nil {
			return err
		}
	}
	return nil
}

// EvaluateBounds binds each finite range datum of d through binder and
// evaluates it with evaluation, filling d.Bound's tuples in demand order.
// It holds nothing; binder takes Engine.mu per datum.
func EvaluateBounds(ctx context.Context, d *Definition, binder BoundBinder, evaluation *exec.Context) error {
	if d.Bound.Strategy == parser.PartitionHash {
		return ctx.Err()
	}
	if d.Bound.Strategy == parser.PartitionList {
		return evaluateListBounds(ctx, d, binder, evaluation)
	}
	for side, raw := range [2][]parser.PartitionDatum{d.Raw.Lower, d.Raw.Upper} {
		tuple := d.Bound.Lower
		if side == 1 {
			tuple = d.Bound.Upper
		}
		finite := 0
		for i, datum := range raw {
			if err := evaluation.Context.Err(); err != nil {
				return err
			}
			tuple[i] = catalog.PartitionDatum{Kind: datum.Kind, Pos: datum.Pos}
			if datum.Kind != 0 {
				continue
			}
			// PostgreSQL advances the key type cursor only for finite datums,
			// including malformed finite entries after an infinite datum.
			node, err := binder.BindBound(datum, d.Parent.Partition.Keys[finite])
			if err != nil {
				return err
			}
			value, err := node.Eval(evaluation)
			if err != nil {
				return err
			}
			if value.IsNull() {
				return sqlError("42P17", "cannot specify NULL in range bound")
			}
			tuple[i].Value = value
			finite++
		}
		var sentinel int8
		for _, datum := range tuple {
			if sentinel != 0 && datum.Kind != sentinel {
				name := "MINVALUE"
				if sentinel > 0 {
					name = "MAXVALUE"
				}
				return expr.ErrorAt("42804", "every bound following "+name+" must also be "+name, datum.Pos)
			}
			if datum.Kind != 0 {
				sentinel = datum.Kind
			}
		}
	}
	return nil
}

// ValidateBounds rejects an empty range bound. Both CREATE and ATTACH use the
// same typed deciding-component diagnostic. ATTACH transforms datums before
// opening its candidate, then invokes this validation after that existing
// relation's eligibility/schema checks. It holds nothing.
func ValidateBounds(ctx context.Context, input sqltype.InputEnvironment, d *Definition) error {
	if d.Bound.DefaultBound {
		return nil
	}
	if d.Bound.Strategy == parser.PartitionHash {
		return nil
	}
	if d.Bound.Strategy == parser.PartitionList {
		return validateListBound(ctx, &d.Parent.Partition.Keys[0], d.Bound)
	}
	order, deciding, err := CompareTuple(ctx, d.Parent.Partition.Keys, d.Bound.Lower, d.Bound.Upper, input)
	if err != nil {
		return err
	}
	if order >= 0 {
		lower, err := TupleSQL(ctx, d.Bound.Lower)
		if err != nil {
			return err
		}
		upper, err := TupleSQL(ctx, d.Bound.Upper)
		if err != nil {
			return err
		}
		diagnostic := expr.ErrorAt("42P17", fmt.Sprintf(`empty range bound specified for partition "%s"`, d.Child.Name()), d.Bound.Lower[deciding].Pos).(*sqlstate.Error)
		diagnostic.Detail = fmt.Sprintf("Specified lower bound (%s) is greater than or equal to upper bound (%s).", lower, upper)
		return diagnostic
	}
	return nil
}

// CheckOverlap rejects a bound of d that overlaps a sibling's in base. It
// holds nothing.
func CheckOverlap(ctx context.Context, input sqltype.InputEnvironment, base catalog.Read, d *Definition) error {
	if d.Bound.Strategy == parser.PartitionHash {
		return checkHashOverlap(ctx, base, d)
	}
	if d.Bound.Strategy == parser.PartitionList {
		return checkListOverlap(ctx, input, base, d)
	}
	count := len(d.Parent.Partition.Children)
	if d.Parent.Partition.DefaultChild != (ids.ObjectAddress{}) {
		count--
	}
	if count == 0 {
		return nil
	}
	// Most hierarchies need no heap allocation. Larger ones require one bounded
	// projection to reproduce the ordered search's last-comparison diagnostic.
	var local [16]Boundary
	bounds := local[:0]
	if count > len(local)/2 {
		bounds = make([]Boundary, 0, 2*count)
	}
	for _, address := range d.Parent.Partition.Children {
		sibling := base.MustRelation(address)
		if sibling.Membership.DefaultBound {
			continue
		}
		bounds = append(bounds,
			Boundary{Tuple: sibling.Membership.Lower, Relation: sibling, Lower: true},
			Boundary{Tuple: sibling.Membership.Upper, Relation: sibling})
	}
	compare := func(a, b Boundary) (int, int, error) {
		order, component, err := CompareTuple(ctx, d.Parent.Partition.Keys, a.Tuple, b.Tuple, input)
		if err == nil && order == 0 && a.Lower != b.Lower {
			order = -1
			if a.Lower {
				order = 1
			}
		}
		return order, component, err
	}
	var sortErr error
	slices.SortFunc(bounds, func(a, b Boundary) int {
		if sortErr != nil {
			return 0
		}
		order, _, err := compare(a, b)
		sortErr = err
		return order
	})
	if sortErr != nil {
		return sortErr
	}
	// A shared endpoint is represented by its exclusive upper boundary, as in
	// PostgreSQL's PartitionBoundInfo. That boundary's following partition is
	// identified by the next upper endpoint, not by a second membership map.
	unique := bounds[:0]
	for _, bound := range bounds {
		if len(unique) != 0 {
			order, _, err := CompareTuple(ctx, d.Parent.Partition.Keys, unique[len(unique)-1].Tuple, bound.Tuple, input)
			if err != nil {
				return err
			}
			if order == 0 {
				continue
			}
		}
		unique = append(unique, bound)
	}
	bounds = unique
	probe := Boundary{Tuple: d.Bound.Lower, Lower: true}
	lo, hi := -1, len(bounds)-1
	lastOrder, lastComponent := 0, 0
	for lo < hi {
		mid := (lo + hi + 1) / 2
		order, component, err := compare(bounds[mid], probe)
		if err != nil {
			return err
		}
		lastOrder, lastComponent = order, component
		if order <= 0 {
			lo = mid
			if order == 0 {
				break
			}
		} else {
			hi = mid - 1
		}
	}
	if lo+1 == len(bounds) {
		return nil
	}
	next := bounds[lo+1]
	position := d.Bound.Lower[lastComponent].Pos
	if !next.Lower {
		// The lower endpoint is already inside a sibling. PostgreSQL exposes
		// the search's final comparison component, with equality at the first.
		if lastOrder == 0 {
			position = d.Bound.Lower[0].Pos
		}
	} else {
		// The lower endpoint is in a gap. Only its next partition can be the
		// first overlap; point to the deciding new upper component.
		order, component, err := compare(next, Boundary{Tuple: d.Bound.Upper})
		if err != nil {
			return err
		}
		if order >= 0 {
			return nil
		}
		position = d.Bound.Upper[component].Pos
	}
	return expr.ErrorAt("42P17", fmt.Sprintf(`partition "%s" would overlap partition "%s"`, d.Child.Name(), next.Relation.Name()), position)
}

// CheckCoverage rejects a UNIQUE or EXCLUDE constraint of the new
// partitioned child whose index does not cover its partition key. stage
// holds the child's indexes. It holds nothing.
func CheckCoverage(view catalog.Read, stage ddl.Stage, child *catalog.Relation) error {
	for _, constraint := range child.Constraints.Unique {
		idx := stage.Get(constraint.Index).(*catalog.Relation)
		if err := index.PartitionUniqueCoverage(view, child, idx.SQLIndex); err != nil {
			return err
		}
	}
	for _, constraint := range child.Constraints.Exclusion {
		idx := stage.Get(constraint.Index).(*catalog.Relation)
		if err := index.PartitionUniqueCoverage(view, child, idx.SQLIndex); err != nil {
			return err
		}
	}
	return nil
}

// CheckDefaultRows rejects a new bound of d that accepts a row of the
// default partition: it scans d.DefaultLeaves in rows, the captured data
// view, evaluating partition keys with evaluation. It holds nothing.
func CheckDefaultRows(ctx context.Context, evaluation *exec.Context, rows *visibility.View, base catalog.Read, d *Definition) error {
	for _, ref := range d.DefaultLeaves {
		leaf, ok := base.Relation(ref.Address)
		if !ok {
			return ids.ErrCatalogReplaced
		}
		layout, err := catalog.InheritanceQueryLayout(d.Parent, leaf)
		if err != nil {
			return err
		}
		mapping := exec.InheritanceQuerySegment{Layout: layout}
		if layout != nil {
			mapping.Scratch = make([]sqltype.Value, len(layout))
		}
		source, err := exec.CaptureSource(ctx, rows, ref)
		if err != nil {
			return err
		}
		for i := range source.Count() {
			var values [32]catalog.PartitionDatum
			key, err := RowKey(d.Parent, mapping.MapValues(source.Row(i)), evaluation, values[:0])
			if err != nil {
				source.Close()
				return err
			}
			contains, err := Contains(ctx, d.Parent.Partition.Keys, d.Bound, key, evaluation.Input)
			if err != nil {
				source.Close()
				return err
			}
			if contains {
				source.Close()
				defaultRelation := base.MustRelation(d.Parent.Partition.DefaultChild)
				diagnostic := sqlError("23514", fmt.Sprintf(`updated partition constraint for default partition "%s" would be violated by some row`, defaultRelation.Name())).(*sqlstate.Error)
				diagnostic.Schema, diagnostic.Table = base.Get(defaultRelation.Namespace()).Name(), defaultRelation.Name()
				return diagnostic
			}
		}
		source.Close()
	}
	return nil
}
