package partition

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

// LIST equality is the admitted key comparator's equality, with a separate
// typed NULL owner. Neither routing nor normalization renders or hashes values.
func listContains(ctx context.Context, key *catalog.PartitionKeyEntry, values []catalog.PartitionDatum, value sqltype.Value, input sqltype.InputEnvironment) (bool, error) {
	for i := range values {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		candidate := &values[i].Value
		if candidate.IsNull() || value.IsNull() {
			if candidate.IsNull() && value.IsNull() {
				return true, nil
			}
			continue
		}
		order, err := key.Comparison.Compare(ctx, *candidate, value, input)
		if err != nil || order == 0 {
			return order == 0, err
		}
	}
	return false, ctx.Err()
}

func evaluateListBounds(ctx context.Context, d *Definition, binder BoundBinder, evaluation *exec.Context) error {
	key := &d.Parent.Partition.Keys[0]
	for _, raw := range d.Raw.Values {
		if err := ctx.Err(); err != nil {
			return err
		}
		node, err := binder.BindBound(raw, *key)
		if err != nil {
			return err
		}
		value, err := node.Eval(evaluation)
		if err != nil {
			return err
		}
		duplicate, err := listContains(ctx, key, d.Bound.Values, value, evaluation.Input)
		if err != nil {
			return err
		}
		if !duplicate {
			d.Bound.Values = append(d.Bound.Values, catalog.PartitionDatum{Value: value, Pos: raw.Pos})
		}
	}
	return ctx.Err()
}

func validateListBound(ctx context.Context, key *catalog.PartitionKeyEntry, bound *catalog.PartitionMembership) error {
	invalid := func() error { return fmt.Errorf("stuntgres: invalid partition hierarchy descriptor") }
	if len(bound.Lower) != 0 || len(bound.Upper) != 0 || bound.Modulus != 0 || bound.Remainder != 0 {
		return invalid()
	}
	if bound.DefaultBound {
		if len(bound.Values) != 0 {
			return invalid()
		}
		return ctx.Err()
	}
	if len(bound.Values) == 0 {
		return invalid()
	}
	for i, datum := range bound.Values {
		if datum.Kind != 0 || datum.Value.Type() != key.Usage.Type {
			return invalid()
		}
		duplicate, err := listContains(ctx, key, bound.Values[:i], datum.Value, sqltype.InputEnvironment{})
		if err != nil {
			return err
		}
		if duplicate {
			return invalid()
		}
	}
	return ctx.Err()
}

// New declaration order selects the first conflicting datum/owner. All bound
// expressions have already executed, even those after an overlapping value.
func checkListOverlap(ctx context.Context, input sqltype.InputEnvironment, view catalog.Read, d *Definition) error {
	for _, datum := range d.Bound.Values {
		for _, address := range d.Parent.Partition.Children {
			sibling := view.MustRelation(address)
			if sibling.Membership.DefaultBound {
				continue
			}
			overlap, err := listContains(ctx, &d.Parent.Partition.Keys[0], sibling.Membership.Values, datum.Value, input)
			if err != nil {
				return err
			}
			if overlap {
				return expr.ErrorAt("42P17", fmt.Sprintf(`partition "%s" would overlap partition "%s"`, d.Child.Name(), sibling.Name()), datum.Pos)
			}
		}
	}
	return ctx.Err()
}
