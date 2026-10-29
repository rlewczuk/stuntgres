package partition

import (
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func (q qualDeparser) listQual(bound *catalog.PartitionMembership) ([]qualification, error) {
	if len(q.keys) != 1 {
		return nil, invalidDeparse()
	}
	values := bound.Values
	if bound.DefaultBound {
		values = nil
		for _, address := range q.parent.Partition.Children {
			if err := q.ctx.Err(); err != nil {
				return nil, err
			}
			child, ok := q.r.View.Relation(address)
			if !ok || child.Membership == nil || child.Membership.Parent != q.parent.Address() {
				return nil, invalidDeparse()
			}
			if !child.Membership.DefaultBound {
				values = append(values, child.Membership.Values...)
			}
		}
	}
	null := false
	finite := make([]catalog.PartitionDatum, 0, len(values))
	for _, datum := range values {
		if err := q.ctx.Err(); err != nil {
			return nil, err
		}
		if datum.Kind != 0 || datum.Value.Type() != q.parent.Partition.Keys[0].Usage.Type {
			return nil, invalidDeparse()
		}
		if datum.Value.IsNull() {
			null = true
		} else {
			finite = append(finite, datum)
		}
	}
	if len(finite) == 0 && !null {
		if bound.DefaultBound {
			return nil, nil
		}
		return nil, invalidDeparse()
	}
	if bound.DefaultBound && len(finite) > 1 {
		if err := q.requireOrdering(0); err != nil {
			return nil, err
		}
		var comparisonError error
		slices.SortStableFunc(finite, func(a, b catalog.PartitionDatum) int {
			if comparisonError != nil {
				return 0
			}
			var order int
			order, comparisonError = q.parent.Partition.Keys[0].Comparison.Compare(q.ctx, a.Value, b.Value, sqltype.InputEnvironment{})
			return order
		})
		if comparisonError != nil {
			return nil, comparisonError
		}
	}
	nullTerm, err := q.nullTest(0, !null)
	if err != nil {
		return nil, err
	}
	result := []qualification{nullTerm}
	if len(finite) != 0 {
		var comparison qualification
		if len(finite) > 1 && !q.parent.Partition.Keys[0].Usage.Type.BaseType().IsArray() {
			left, op, namespace, err := q.comparisonOperands(0, 3)
			if err != nil {
				return nil, err
			}
			items := make([]expr.Expr, len(finite))
			for i, datum := range finite {
				items[i], err = q.datum(0, datum)
				if err != nil {
					return nil, err
				}
			}
			comparison, err = q.leaf(plan.ScalarArrayComparisonExpr{Info: expr.Info{Usage: q.boolean.Usage()}, Left: left, Items: items, Operator: op.Name(), Namespace: namespace})
			if err != nil {
				return nil, err
			}
		} else {
			terms := make([]qualification, len(finite))
			for i, datum := range finite {
				terms[i], err = q.compare(0, 3, datum)
				if err != nil {
					return nil, err
				}
			}
			comparison = qualGroup("OR", terms)
		}
		if null {
			result = []qualification{qualGroup("OR", []qualification{nullTerm, comparison})}
		} else {
			result = append(result, comparison)
		}
	}
	if bound.DefaultBound {
		result = []qualification{{op: "NOT", args: []qualification{qualGroup("AND", result)}}}
	}
	return result, nil
}
