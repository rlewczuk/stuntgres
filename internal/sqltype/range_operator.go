package sqltype

import (
	"context"
	"fmt"
)

// RangeOperator identifies the closed PG18 range/multirange operator signatures.
type RangeOperator uint8

const (
	RangeContains RangeOperator = iota
	RangeContained
	RangeOverlap
	RangeLeft
	RangeRight
	RangeNotRight
	RangeNotLeft
	RangeAdjacent
	RangeUnion
	RangeIntersection
	RangeDifference
)

func RangeOperatorKind(name string) (RangeOperator, bool) {
	switch name {
	case "@>":
		return RangeContains, true
	case "<@":
		return RangeContained, true
	case "&&":
		return RangeOverlap, true
	case "<<":
		return RangeLeft, true
	case ">>":
		return RangeRight, true
	case "&<":
		return RangeNotRight, true
	case "&>":
		return RangeNotLeft, true
	case "-|-":
		return RangeAdjacent, true
	case "+":
		return RangeUnion, true
	case "*":
		return RangeIntersection, true
	case "-":
		return RangeDifference, true
	}
	return 0, false
}
func (o RangeOperator) Name() string {
	switch o {
	case RangeContains:
		return "@>"
	case RangeContained:
		return "<@"
	case RangeOverlap:
		return "&&"
	case RangeLeft:
		return "<<"
	case RangeRight:
		return ">>"
	case RangeNotRight:
		return "&<"
	case RangeNotLeft:
		return "&>"
	case RangeAdjacent:
		return "-|-"
	case RangeUnion:
		return "+"
	case RangeIntersection:
		return "*"
	case RangeDifference:
		return "-"
	}
	return ""
}
func operatorRangeType(d *Descriptor) *Descriptor {
	if d == nil || !d.Concrete() {
		return nil
	}
	if isRange(d.id) {
		return d
	}
	if d.validMultirangeAuthority() {
		return d.multirangeRange
	}
	return nil
}

// RangeOperatorResult admits physical operands only, with exact shared authority.
// Domain unwrapping and UNKNOWN contextualization belong to the existing binder.
func RangeOperatorResult(op RangeOperator, left, right *Descriptor) *Descriptor {
	if left == nil || right == nil || left.IsDomain() || right.IsDomain() {
		return nil
	}
	lr, rr := operatorRangeType(left), operatorRangeType(right)
	r := lr
	if r == nil {
		r = rr
	}
	if r == nil {
		return nil
	}
	if op >= RangeUnion {
		if op > RangeDifference || left != right || lr == nil {
			return nil
		}
		return left
	}
	if lr != nil && rr != nil {
		if lr != rr {
			return nil
		}
		return r.rangeBoolean
	}
	if op == RangeContains && lr != nil && right == r.rangeSubtype || op == RangeContained && rr != nil && left == r.rangeSubtype {
		return r.rangeBoolean
	}
	return nil
}

type rangeOperandKind uint8

const (
	rangeElementOperand rangeOperandKind = iota
	rangeSingleOperand
	rangeMultiOperand
)

type RangeOperatorPlan struct {
	left, right, result, rangeType *Descriptor
	operation                      RangeOperator
	leftKind, rightKind            rangeOperandKind
}

func BindRangeOperator(op RangeOperator, result, left, right Usage) (RangeOperatorPlan, error) {
	expected := RangeOperatorResult(op, left.Type, right.Type)
	if expected == nil || result != expected.Usage() || left != left.Type.Usage() || right != right.Type.Usage() {
		return RangeOperatorPlan{}, fmt.Errorf("invalid range operator authority")
	}
	r := operatorRangeType(left.Type)
	if r == nil {
		r = operatorRangeType(right.Type)
	}
	kind := func(d *Descriptor) rangeOperandKind {
		if d == r {
			return rangeSingleOperand
		}
		if d == r.multirangeCompanion {
			return rangeMultiOperand
		}
		return rangeElementOperand
	}
	return RangeOperatorPlan{left.Type, right.Type, expected, r, op, kind(left.Type), kind(right.Type)}, nil
}
func (p RangeOperatorPlan) Operation() RangeOperator { return p.operation }
func (p RangeOperatorPlan) Name() string             { return p.operation.Name() }
func (p RangeOperatorPlan) ResultUsage() Usage       { return p.result.Usage() }
func (p RangeOperatorPlan) Evaluate(ctx context.Context, values [2]Value) (Value, error) {
	if ctx == nil {
		return Value{}, fmt.Errorf("missing range operator context")
	}
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if p.result == nil || values[0].typ != p.left || values[1].typ != p.right {
		return Value{}, fmt.Errorf("foreign range operator value")
	}
	if values[0].null || values[1].null {
		return Null(p.result), nil
	}
	if p.operation >= RangeUnion {
		return p.evaluateSet(ctx, values[0], values[1])
	}
	a, b := &values[0], &values[1]
	op := p.operation
	ak, bk := p.leftKind, p.rightKind
	if op == RangeContained {
		a, b = b, a
		ak, bk = bk, ak
		op = RangeContains
	}
	var aa, bb []rangeDatum
	var singleA, singleB [1]rangeDatum
	if ak != rangeElementOperand {
		aa = operatorMembers(a, ak, &singleA)
	}
	if bk != rangeElementOperand {
		bb = operatorMembers(b, bk, &singleB)
	}
	r := p.rangeType
	if op == RangeContains && bk == rangeElementOperand {
		bound := rangeBound{magnitude: b.integer}
		if b.typ.id == Numeric {
			bound = rangeBound{b.text, b.exponent, b.scale, b.negative, b.special}
		}
		// Binary search the first member whose upper endpoint can contain the value.
		lo, hi := 0, len(aa)
		for lo < hi {
			mid := lo + (hi-lo)/2
			x := aa[mid]
			cmp := r.compareRangeBounds(x.upper, bound)
			if x.flags&rangeUpperUnbounded == 0 && (cmp < 0 || cmp == 0 && x.flags&rangeUpperInclusive == 0) {
				lo = mid + 1
			} else {
				hi = mid
			}
		}
		yes := false
		if lo < len(aa) {
			x := aa[lo]
			cmp := r.compareRangeBounds(x.lower, bound)
			yes = x.flags&rangeLowerUnbounded != 0 || cmp < 0 || cmp == 0 && x.flags&rangeLowerInclusive != 0
		}
		return Boolean(p.result, yes), nil
	}
	yes := false
	switch op {
	case RangeContains:
		yes = true
		i := 0
		for j, x := range bb {
			if j&255 == 0 {
				if err := ctx.Err(); err != nil {
					return Value{}, err
				}
			}
			for i < len(aa) && r.compareRangeEndpoint(aa[i], false, x, true) < 0 {
				i++
				if i&255 == 0 {
					if err := ctx.Err(); err != nil {
						return Value{}, err
					}
				}
			}
			if i == len(aa) || !r.rangeContains(aa[i], x) {
				yes = false
				break
			}
		}
	case RangeOverlap:
		i, j := 0, 0
		for i < len(aa) && j < len(bb) {
			if (i+j)&255 == 0 {
				if err := ctx.Err(); err != nil {
					return Value{}, err
				}
			}
			if r.rangeOverlaps(aa[i], bb[j]) {
				yes = true
				break
			}
			if r.compareRangeEndpoint(aa[i], false, bb[j], false) < 0 {
				i++
			} else {
				j++
			}
		}
	default:
		if len(aa) != 0 && len(bb) != 0 {
			firstA, lastA, firstB, lastB := aa[0], aa[len(aa)-1], bb[0], bb[len(bb)-1]
			switch op {
			case RangeLeft:
				yes = r.compareRangeEndpoint(lastA, false, firstB, true) < 0
			case RangeRight:
				yes = r.compareRangeEndpoint(firstA, true, lastB, false) > 0
			case RangeNotRight:
				yes = r.compareRangeEndpoint(lastA, false, lastB, false) <= 0
			case RangeNotLeft:
				yes = r.compareRangeEndpoint(firstA, true, firstB, true) >= 0
			case RangeAdjacent:
				yes = r.rangeBoundsAdjacent(lastA, firstB) || r.rangeBoundsAdjacent(lastB, firstA)
			}
		}
	}
	return Boolean(p.result, yes), nil
}
func operatorMembers(v *Value, kind rangeOperandKind, single *[1]rangeDatum) []rangeDatum {
	if kind == rangeMultiOperand {
		return v.multirange.members
	}
	if v.rangeDatum.flags&rangeEmpty != 0 {
		return nil
	}
	single[0] = v.rangeDatum
	return single[:]
}
func (r *Descriptor) rangeContains(a, b rangeDatum) bool {
	return r.compareRangeEndpoint(a, true, b, true) <= 0 && r.compareRangeEndpoint(a, false, b, false) >= 0
}
func (r *Descriptor) rangeOverlaps(a, b rangeDatum) bool {
	return r.compareRangeEndpoint(a, true, b, false) <= 0 && r.compareRangeEndpoint(b, true, a, false) <= 0
}

// Complementary bounds identify adjacency, including discrete DATE infinities.
func (r *Descriptor) rangeBoundsAdjacent(a, b rangeDatum) bool {
	if a.flags&rangeUpperUnbounded != 0 || b.flags&rangeLowerUnbounded != 0 {
		return false
	}
	cmp := r.compareRangeBounds(a.upper, b.lower)
	if cmp == 0 {
		return (a.flags&rangeUpperInclusive != 0) != (b.flags&rangeLowerInclusive != 0)
	}
	if cmp > 0 || r.id == NumRange || r.id == TimestampRange || r.id == TimestamptzRange {
		return false
	}
	gap := rangeDatum{lower: a.upper, upper: b.lower}
	if a.flags&rangeUpperInclusive == 0 {
		gap.flags |= rangeLowerInclusive
	}
	if b.flags&rangeLowerInclusive == 0 {
		gap.flags |= rangeUpperInclusive
	}
	canonical, err := r.canonicalRange(gap)
	return err == nil && canonical.flags&rangeEmpty != 0
}
func (r *Descriptor) compareRangeValues(a, b rangeDatum) int {
	ae, be := a.flags&rangeEmpty != 0, b.flags&rangeEmpty != 0
	if ae {
		if be {
			return 0
		}
		return -1
	}
	if be {
		return 1
	}
	return r.compareRanges(a, b)
}
func (r *Descriptor) compareMultirangeValues(ctx context.Context, a, b []rangeDatum) (int, error) {
	for i := 0; i < len(a) && i < len(b); i++ {
		if i&255 == 0 {
			if err := ctx.Err(); err != nil {
				return 0, err
			}
		}
		if cmp := r.compareRangeValues(a[i], b[i]); cmp != 0 {
			return cmp, nil
		}
	}
	if len(a) < len(b) {
		return -1, nil
	}
	if len(a) > len(b) {
		return 1, nil
	}
	return 0, nil
}
