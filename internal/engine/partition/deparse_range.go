package partition

import (
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/sqltype"
)

func (q qualDeparser) rangeQual(bound *catalog.PartitionMembership, forDefault bool) ([]qualification, error) {
	if err := q.ctx.Err(); err != nil {
		return nil, err
	}
	if bound.DefaultBound {
		return q.rangeDefault()
	}
	n := len(q.keys)
	if len(bound.Lower) != n || len(bound.Upper) != n {
		return nil, invalidDeparse()
	}
	for _, tuple := range [][]catalog.PartitionDatum{bound.Lower, bound.Upper} {
		for i, datum := range tuple {
			if datum.Kind < -1 || datum.Kind > 1 {
				return nil, invalidDeparse()
			}
			if datum.Kind == 0 {
				if _, err := q.datum(i, datum); err != nil {
					return nil, err
				}
			}
		}
	}
	var result []qualification
	if !forDefault {
		var err error
		result, err = q.nullTests()
		if err != nil {
			return nil, err
		}
	}
	// Factor a common finite prefix, exactly as get_qual_for_range does.
	first := 0
	for ; first < n; first++ {
		lower, upper := bound.Lower[first], bound.Upper[first]
		if lower.Kind != 0 || upper.Kind != 0 {
			break
		}
		if err := q.requireOrdering(first); err != nil {
			return nil, err
		}
		order, err := q.parent.Partition.Keys[first].Comparison.Compare(q.ctx, lower.Value, upper.Value, sqltype.InputEnvironment{})
		if err != nil {
			return nil, err
		}
		if order != 0 {
			break
		}
		if first == n-1 {
			return nil, invalidDeparse()
		}
		term, err := q.compare(first, 3, lower)
		if err != nil {
			return nil, err
		}
		result = append(result, term)
	}
	for _, lower := range []bool{true, false} {
		tuple := bound.Upper
		if lower {
			tuple = bound.Lower
		}
		var arms []qualification
		for end := first; end < n; end++ {
			if err := q.ctx.Err(); err != nil {
				return nil, err
			}
			if tuple[end].Kind != 0 {
				break
			}
			terms := make([]qualification, 0, end-first+1)
			for i := first; i <= end; i++ {
				strategy := int16(3) // equalities preceding the decisive key
				if i == end {
					if lower {
						strategy = 5
						if end == n-1 || tuple[end+1].Kind < 0 {
							strategy = 4
						}
					} else {
						strategy = 1
						if end < n-1 && tuple[end+1].Kind > 0 {
							strategy = 2
						}
					}
				}
				term, err := q.compare(i, strategy, tuple[i])
				if err != nil {
					return nil, err
				}
				terms = append(terms, term)
			}
			arms = append(arms, qualGroup("AND", terms))
			if end < n-1 && tuple[end+1].Kind != 0 {
				break
			}
		}
		if len(arms) != 0 {
			result = append(result, qualGroup("OR", arms))
		}
	}
	// A recursively constructed full-unbounded sibling still contributes its
	// null tests; DEFAULT adds its own shared tests outside the sibling union.
	if len(result) == 0 {
		return q.nullTests()
	}
	return result, nil
}

func (q qualDeparser) rangeDefault() ([]qualification, error) {
	bounds := make([]*catalog.PartitionMembership, 0, len(q.parent.Partition.Children))
	for _, address := range q.parent.Partition.Children {
		if err := q.ctx.Err(); err != nil {
			return nil, err
		}
		child, ok := q.r.View.Relation(address)
		if !ok || child.Membership == nil || child.Membership.Parent != q.parent.Address() {
			return nil, invalidDeparse()
		}
		if !child.Membership.DefaultBound {
			if len(child.Membership.Lower) != len(q.keys) {
				return nil, invalidDeparse()
			}
			bounds = append(bounds, child.Membership)
		}
	}
	if len(bounds) == 0 {
		return nil, nil
	}
	if len(bounds) > 1 {
		for i := range q.keys {
			if err := q.requireOrdering(i); err != nil {
				return nil, err
			}
		}
	}
	var comparisonError error
	slices.SortStableFunc(bounds, func(a, b *catalog.PartitionMembership) int {
		if comparisonError != nil {
			return 0
		}
		var order int
		order, _, comparisonError = CompareTuple(q.ctx, q.parent.Partition.Keys, a.Lower, b.Lower, sqltype.InputEnvironment{})
		return order
	})
	if comparisonError != nil {
		return nil, comparisonError
	}
	arms := make([]qualification, 0, len(bounds))
	for _, bound := range bounds {
		local, err := q.rangeQual(bound, true)
		if err != nil {
			return nil, err
		}
		arms = append(arms, qualGroup("AND", local))
	}
	shared, err := q.nullTests()
	if err != nil {
		return nil, err
	}
	shared = append(shared, qualGroup("OR", arms))
	return []qualification{{op: "NOT", args: []qualification{qualGroup("AND", shared)}}}, nil
}
