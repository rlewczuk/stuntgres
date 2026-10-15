package sqltype

import (
	"cmp"
	"context"
	"fmt"
)

// Equal compares bound authority structurally, not immutable child addresses.
func (p Comparison) Equal(other Comparison) bool {
	child, next := p.element, other.element
	p.element, other.element = nil, nil
	if p != other {
		return false
	}
	if child == nil || next == nil {
		return child == next
	}
	return child.Equal(*next)
}

func (p ComparisonPredicate) Equal(other ComparisonPredicate) bool {
	return p.result == other.result && p.operator == other.operator && p.operands.Equal(other.operands)
}

func bindArrayComparisonElement(array *Descriptor, collation CollationRef, conflict bool) (*Comparison, error) {
	element := array.Element()
	base := element.BaseType()
	var child Comparison
	switch base.ID() {
	case JSON, JSONPath:
		child = Comparison{left: element, right: element, missingEquality: true, missingOrdering: true}
	case ACLItem:
		child = Comparison{left: element, right: element, missingOrdering: true}
	default:
		var err error
		child, err = BindComparison(base, base, collation, conflict)
		if err != nil {
			return nil, err
		}
		child.left, child.right = element, element
	}
	return &child, nil
}

func (p Comparison) demandEquality() error {
	if p.missingEquality {
		return failure("42883", "could not identify an equality operator for type "+p.left.DiagnosticName())
	}
	return nil
}
func (p Comparison) demandOrdering() error {
	if p.missingOrdering {
		return failure("42883", "could not identify a comparison function for type "+p.left.DiagnosticName())
	}
	return nil
}

// EqualValues compares non-NULL datums. SQL strictness belongs to the predicate;
// NULL array elements instead compare equal to one another.
func (p Comparison) EqualValues(ctx context.Context, a, b Value, input InputEnvironment) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if a.typ != p.left || b.typ != p.right || a.null || b.null || !p.left.Concrete() {
		return false, fmt.Errorf("invalid comparison values")
	}
	if err := p.demandEquality(); err != nil {
		return false, err
	}
	if p.element == nil {
		if p.left.BaseType().ID() == ACLItem {
			return aclEquality(ctx, a, b)
		}
		sign, err := p.Compare(ctx, a, b, input)
		return sign == 0, err
	}
	if a.array == nil || b.array == nil {
		return false, fmt.Errorf("missing array payload")
	}
	if a.ArrayRank() != b.ArrayRank() {
		return false, nil
	}
	for i := range a.ArrayRank() {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if a.ArrayDimension(i) != b.ArrayDimension(i) {
			return false, nil
		}
	}
	if err := p.element.demandEquality(); err != nil {
		return false, err
	}
	for i := range a.ArrayLen() {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		left, right := a.ArrayElement(i), b.ArrayElement(i)
		if left.null || right.null {
			if left.null != right.null {
				return false, nil
			}
			continue
		}
		equal, err := p.element.EqualValues(ctx, left, right, input)
		if err != nil || !equal {
			return equal, err
		}
	}
	return true, nil
}

func (p Comparison) compareArrays(ctx context.Context, a, b Value, input InputEnvironment) (int, error) {
	if a.array == nil || b.array == nil {
		return 0, fmt.Errorf("missing array payload")
	}
	if err := p.element.demandOrdering(); err != nil {
		return 0, err
	}
	for i := range min(a.ArrayLen(), b.ArrayLen()) {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		left, right := a.ArrayElement(i), b.ArrayElement(i)
		if left.null || right.null {
			if left.null != right.null {
				if left.null {
					return 1, nil
				}
				return -1, nil
			}
			continue
		}
		sign, err := p.element.Compare(ctx, left, right, input)
		if err != nil || sign != 0 {
			return sign, err
		}
	}
	if sign := cmp.Compare(a.ArrayLen(), b.ArrayLen()); sign != 0 {
		return sign, nil
	}
	if sign := cmp.Compare(a.ArrayRank(), b.ArrayRank()); sign != 0 {
		return sign, nil
	}
	for i := range a.ArrayRank() {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		if sign := cmp.Compare(a.ArrayDimension(i).Length, b.ArrayDimension(i).Length); sign != 0 {
			return sign, nil
		}
	}
	for i := range a.ArrayRank() {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		if sign := cmp.Compare(a.ArrayDimension(i).Lower, b.ArrayDimension(i).Lower); sign != 0 {
			return sign, nil
		}
	}
	return 0, nil
}
