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
	if d.IsRange() {
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
		if r.customRange != nil {
			bound = rangeBound{value: b}
		} else if b.typ.id == Numeric {
			bound = rangeBound{coefficient: b.text, magnitude: b.exponent, scale: b.scale, negative: b.negative, special: b.special}
		}
		lo, hi := 0, len(aa)
		for lo < hi {
			if err := ctx.Err(); err != nil {
				return Value{}, err
			}
			mid := lo + (hi-lo)/2
			x := aa[mid]
			cmp := 1
			if x.flags&rangeUpperUnbounded == 0 {
				var err error
				cmp, err = r.compareRangeBounds(ctx, x.upper, bound)
				if err != nil {
					return Value{}, err
				}
			}
			if cmp < 0 || cmp == 0 && x.flags&rangeUpperInclusive == 0 {
				lo = mid + 1
			} else {
				hi = mid
			}
		}
		yes := false
		if lo < len(aa) {
			x := aa[lo]
			if x.flags&rangeLowerUnbounded != 0 {
				yes = true
			} else {
				cmp, err := r.compareRangeBounds(ctx, x.lower, bound)
				if err != nil {
					return Value{}, err
				}
				yes = cmp < 0 || cmp == 0 && x.flags&rangeLowerInclusive != 0
			}
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
			for i < len(aa) {
				cmp, err := r.compareRangeEndpoint(ctx, aa[i], false, x, true)
				if err != nil {
					return Value{}, err
				}
				if cmp >= 0 {
					break
				}
				i++
				if i&255 == 0 {
					if err := ctx.Err(); err != nil {
						return Value{}, err
					}
				}
			}
			if i == len(aa) {
				yes = false
				break
			}
			contains, err := r.rangeContains(ctx, aa[i], x)
			if err != nil {
				return Value{}, err
			}
			if !contains {
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
			overlaps, err := r.rangeOverlaps(ctx, aa[i], bb[j])
			if err != nil {
				return Value{}, err
			}
			if overlaps {
				yes = true
				break
			}
			cmp, err := r.compareRangeEndpoint(ctx, aa[i], false, bb[j], false)
			if err != nil {
				return Value{}, err
			}
			if cmp < 0 {
				i++
			} else {
				j++
			}
		}
	default:
		if len(aa) != 0 && len(bb) != 0 {
			firstA, lastA, firstB, lastB := aa[0], aa[len(aa)-1], bb[0], bb[len(bb)-1]
			var cmp int
			var err error
			switch op {
			case RangeLeft:
				cmp, err = r.compareRangeEndpoint(ctx, lastA, false, firstB, true)
				yes = cmp < 0
			case RangeRight:
				cmp, err = r.compareRangeEndpoint(ctx, firstA, true, lastB, false)
				yes = cmp > 0
			case RangeNotRight:
				cmp, err = r.compareRangeEndpoint(ctx, lastA, false, lastB, false)
				yes = cmp <= 0
			case RangeNotLeft:
				cmp, err = r.compareRangeEndpoint(ctx, firstA, true, firstB, true)
				yes = cmp >= 0
			case RangeAdjacent:
				yes, err = r.rangeBoundsAdjacent(ctx, lastA, firstB)
				if err == nil && !yes {
					yes, err = r.rangeBoundsAdjacent(ctx, lastB, firstA)
				}
			}
			if err != nil {
				return Value{}, err
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
func (r *Descriptor) rangeContains(ctx context.Context, a, b rangeDatum) (bool, error) {
	lo, err := r.compareRangeEndpoint(ctx, a, true, b, true)
	if err != nil || lo > 0 {
		return false, err
	}
	hi, err := r.compareRangeEndpoint(ctx, a, false, b, false)
	return hi >= 0, err
}
func (r *Descriptor) rangeOverlaps(ctx context.Context, a, b rangeDatum) (bool, error) {
	lo, err := r.compareRangeEndpoint(ctx, a, true, b, false)
	if err != nil || lo > 0 {
		return false, err
	}
	hi, err := r.compareRangeEndpoint(ctx, b, true, a, false)
	return hi <= 0, err
}

// Complementary bounds identify adjacency, including discrete DATE infinities.
func (r *Descriptor) rangeBoundsAdjacent(ctx context.Context, a, b rangeDatum) (bool, error) {
	if a.flags&rangeUpperUnbounded != 0 || b.flags&rangeLowerUnbounded != 0 {
		return false, nil
	}
	cmp, err := r.compareRangeBounds(ctx, a.upper, b.lower)
	if err != nil {
		return false, err
	}
	if cmp == 0 {
		return (a.flags&rangeUpperInclusive != 0) != (b.flags&rangeLowerInclusive != 0), nil
	}
	if cmp > 0 || r.customRange != nil || r.id == NumRange || r.id == TimestampRange || r.id == TimestamptzRange {
		return false, nil
	}
	gap := rangeDatum{lower: a.upper, upper: b.lower}
	if a.flags&rangeUpperInclusive == 0 {
		gap.flags |= rangeLowerInclusive
	}
	if b.flags&rangeLowerInclusive == 0 {
		gap.flags |= rangeUpperInclusive
	}
	canonical, err := r.canonicalRange(ctx, gap, false)
	return canonical.flags&rangeEmpty != 0, err
}
func (r *Descriptor) compareRangeValues(ctx context.Context, a, b rangeDatum) (int, error) {
	ae, be := a.flags&rangeEmpty != 0, b.flags&rangeEmpty != 0
	if ae {
		if be {
			return 0, nil
		}
		return -1, nil
	}
	if be {
		return 1, nil
	}
	return r.compareRanges(ctx, a, b)
}
func (r *Descriptor) compareMultirangeValues(ctx context.Context, a, b []rangeDatum) (int, error) {
	for i := 0; i < len(a) && i < len(b); i++ {
		if i&255 == 0 {
			if err := ctx.Err(); err != nil {
				return 0, err
			}
		}
		if cmp, err := r.compareRangeValues(ctx, a[i], b[i]); err != nil || cmp != 0 {
			return cmp, err
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
