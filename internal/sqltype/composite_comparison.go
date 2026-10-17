package sqltype

import (
	"context"
	"fmt"
)

// A record operation admits the RECORD signature before inspecting its fields.
// Freeze each possible field operation once, but demand it only when reached.
// The descriptor's members are already the ordered live projection: dropped
// attribute numbers do not create comparison fields or affect column diagnostics.
type compositeComparison struct {
	fields        []compositeFieldComparison
	countMismatch bool
	equality      bool
	ordering      bool
}

type compositeFieldComparison struct {
	operands     Comparison
	err          error
	typeMismatch bool
}

func bindCompositeComparison(left, right *Descriptor, _ CollationRef, _ bool) *compositeComparison {
	left, right = left.BaseType(), right.BaseType()
	p := &compositeComparison{
		fields:        make([]compositeFieldComparison, min(left.CompositeLen(), right.CompositeLen())),
		countMismatch: left.CompositeLen() != right.CompositeLen(),
		equality:      true,
		ordering:      true,
	}
	for i := range p.fields {
		l, r := left.CompositeMember(i).Usage, right.CompositeMember(i).Usage
		field := &p.fields[i]
		// Identity is checked before provider demand or the field NULL shortcut.
		// In particular integer/bigint and domain/base are not record casts.
		if l.Type.ID() != r.Type.ID() {
			field.typeMismatch = true
			field.operands = Comparison{left: l.Type, right: r.Type}
			p.equality, p.ordering = false, false
			continue
		}
		ref, conflict := l.Collation, false
		if ref != r.Collation {
			conflict = true
		}
		field.operands, field.err = bindComparisonLeaf(l.Type, r.Type, ref, conflict)
		if field.err != nil {
			p.equality, p.ordering = false, false
		} else {
			p.equality = p.equality && !field.operands.missingEquality
			p.ordering = p.ordering && !field.operands.missingOrdering
		}
	}
	return p
}

// Leaf lookup is different from scalar overload resolution. Record members and
// array elements retain exact identities; no scalar implicit promotion occurs.
func bindComparisonLeaf(left, right *Descriptor, collation CollationRef, conflict bool) (Comparison, error) {
	baseLeft, baseRight := left.BaseType(), right.BaseType()
	equality, ordering := ComparisonCapabilities(baseLeft)
	p := Comparison{left: left, right: right, missingEquality: !equality, missingOrdering: !ordering}
	if !equality && !ordering {
		return p, nil
	}
	// ACLITEM's existing equality provider is intentionally not an ordering
	// provider, and therefore does not pass through BindComparison.
	if baseLeft.ID() == ACLItem {
		return p, nil
	}
	bound, err := BindComparison(baseLeft, baseRight, collation, conflict)
	if err != nil {
		return Comparison{}, err
	}
	bound.left, bound.right = left, right
	bound.missingEquality, bound.missingOrdering = !equality, !ordering
	// typcache.c verifies callable eligibility before handing a named record
	// or array provider to its container. Direct record operations do not
	// perform this preflight, and anonymous RECORD deliberately assumes both
	// capabilities because its occurrence shape is not a catalog type.
	if bound.composite != nil && baseLeft.ID() != 2249 {
		bound.missingEquality = bound.missingEquality || !bound.composite.equality
		bound.missingOrdering = bound.missingOrdering || !bound.composite.ordering
	}
	if bound.element != nil {
		bound.missingEquality = bound.missingEquality || bound.element.missingEquality
		bound.missingOrdering = bound.missingOrdering || bound.element.missingOrdering
	}
	return bound, nil
}

func (p *compositeComparison) Equal(other *compositeComparison) bool {
	if p == nil || other == nil {
		return p == other
	}
	if p.countMismatch != other.countMismatch || p.equality != other.equality || p.ordering != other.ordering || len(p.fields) != len(other.fields) {
		return false
	}
	for i, field := range p.fields {
		next := other.fields[i]
		if field.typeMismatch != next.typeMismatch || (field.err == nil) != (next.err == nil) {
			return false
		}
		if field.err != nil {
			if field.err.Error() != next.err.Error() {
				return false
			}
		} else if !field.operands.Equal(next.operands) {
			return false
		}
	}
	return true
}

// compareComposites returns zero for equality and a nonzero value for inequality
// when ordering is false. Only ordering mode promises the sign of that value.
// Whole-value SQL NULL strictness belongs to ComparisonPredicate, not records.
func (p Comparison) compareComposites(ctx context.Context, a, b Value, input InputEnvironment, ordering bool) (int, error) {
	if a.composite == nil || b.composite == nil || p.composite == nil || a.CompositeLen() != p.left.BaseType().CompositeLen() || b.CompositeLen() != p.right.BaseType().CompositeLen() {
		return 0, fmt.Errorf("invalid composite comparison payload")
	}
	for i, field := range p.composite.fields {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		if field.typeMismatch {
			return 0, failure("42804", fmt.Sprintf("cannot compare dissimilar column types %s and %s at record column %d", field.operands.left.enumDiagnosticName(input), field.operands.right.enumDiagnosticName(input), i+1))
		}
		if field.err != nil {
			return 0, field.err
		}
		var err error
		if ordering {
			err = field.operands.demandOrdering(input)
		} else {
			err = field.operands.demandEquality(input)
		}
		if err != nil {
			return 0, err
		}
		left, right := a.CompositeField(i), b.CompositeField(i)
		if left.null || right.null {
			if left.null != right.null {
				if left.null {
					return 1, nil
				}
				return -1, nil
			}
			continue
		}
		if ordering {
			sign, err := field.operands.Compare(ctx, left, right, input)
			if err != nil || sign != 0 {
				return sign, err
			}
		} else {
			equal, err := field.operands.EqualValues(ctx, left, right, input)
			if err != nil || !equal {
				return 1, err
			}
		}
	}
	if p.composite.countMismatch {
		return 0, failure("42804", "cannot compare record types with different numbers of columns")
	}
	return 0, nil
}
