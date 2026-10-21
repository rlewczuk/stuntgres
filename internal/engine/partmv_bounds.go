package engine

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
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// EvaluateBounds binds each finite range datum of d through binder and
// evaluates it with evaluation, filling d.Bound's tuples in demand order.
// It holds nothing; binder takes Engine.mu per datum.
func EvaluateBounds(ctx context.Context, d *partition.Definition, binder partition.BoundBinder, evaluation *exec.Context) error {
	if d.Bound.Strategy == parser.PartitionHash {
		return ctx.Err()
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
func ValidateBounds(ctx context.Context, input sqltype.InputEnvironment, d *partition.Definition) error {
	if d.Bound.DefaultBound {
		return nil
	}
	if d.Bound.Strategy == parser.PartitionHash {
		return nil
	}
	order, deciding, err := partition.CompareTuple(ctx, d.Parent.Partition.Keys, d.Bound.Lower, d.Bound.Upper, input)
	if err != nil {
		return err
	}
	if order >= 0 {
		lower, err := partition.TupleSQL(ctx, d.Bound.Lower)
		if err != nil {
			return err
		}
		upper, err := partition.TupleSQL(ctx, d.Bound.Upper)
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
func CheckOverlap(ctx context.Context, input sqltype.InputEnvironment, base catalog.Read, d *partition.Definition) error {
	if d.Bound.Strategy == parser.PartitionHash {
		return checkHashOverlap(ctx, base, d)
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
	var local [16]partition.Boundary
	bounds := local[:0]
	if count > len(local)/2 {
		bounds = make([]partition.Boundary, 0, 2*count)
	}
	for _, address := range d.Parent.Partition.Children {
		sibling := base.MustRelation(address)
		if sibling.Membership.DefaultBound {
			continue
		}
		bounds = append(bounds,
			partition.Boundary{Tuple: sibling.Membership.Lower, Relation: sibling, Lower: true},
			partition.Boundary{Tuple: sibling.Membership.Upper, Relation: sibling})
	}
	compare := func(a, b partition.Boundary) (int, int, error) {
		order, component, err := partition.CompareTuple(ctx, d.Parent.Partition.Keys, a.Tuple, b.Tuple, input)
		if err == nil && order == 0 && a.Lower != b.Lower {
			order = -1
			if a.Lower {
				order = 1
			}
		}
		return order, component, err
	}
	var sortErr error
	slices.SortFunc(bounds, func(a, b partition.Boundary) int {
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
			order, _, err := partition.CompareTuple(ctx, d.Parent.Partition.Keys, unique[len(unique)-1].Tuple, bound.Tuple, input)
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
	probe := partition.Boundary{Tuple: d.Bound.Lower, Lower: true}
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
		order, component, err := compare(next, partition.Boundary{Tuple: d.Bound.Upper})
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
func CheckCoverage(stage ddl.Stage, child *catalog.Relation) error {
	for _, constraint := range child.Constraints.Unique {
		idx := stage.Get(constraint.Index).(*catalog.Relation)
		if err := index.PartitionUniqueCoverage(child, idx.SQLIndex); err != nil {
			return err
		}
	}
	for _, constraint := range child.Constraints.Exclusion {
		idx := stage.Get(constraint.Index).(*catalog.Relation)
		if err := index.PartitionUniqueCoverage(child, idx.SQLIndex); err != nil {
			return err
		}
	}
	return nil
}

// CheckDefaultRows rejects a new bound of d that accepts a row of the
// default partition: it scans d.DefaultLeaves in rows, the captured data
// view, evaluating partition keys with evaluation. It holds nothing.
func CheckDefaultRows(ctx context.Context, evaluation *exec.Context, rows *visibility.View, base catalog.Read, d *partition.Definition) error {
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
			key, err := partition.RowKey(d.Parent, mapping.MapValues(source.Row(i)), evaluation, values[:0])
			if err != nil {
				source.Close()
				return err
			}
			contains, err := partition.Contains(ctx, d.Parent.Partition.Keys, d.Bound, key, evaluation.Input)
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
