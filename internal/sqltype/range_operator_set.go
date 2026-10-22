package sqltype

import "context"

func rangeFromBounds(lower, upper rangeDatum) rangeDatum {
	return rangeDatum{lower: lower.lower, upper: upper.upper, flags: lower.flags&(rangeLowerInclusive|rangeLowerUnbounded) | upper.flags&(rangeUpperInclusive|rangeUpperUnbounded)}
}
func (r *Descriptor) unionRanges(a, b rangeDatum) rangeDatum {
	lo, hi := a, a
	if r.compareRangeEndpoint(a, true, b, true) >= 0 {
		lo = b
	}
	if r.compareRangeEndpoint(a, false, b, false) <= 0 {
		hi = b
	}
	return rangeFromBounds(lo, hi)
}

// Subtract one member into at most two canonical survivors. The caller decides
// whether two pieces are representable (multirange) or an SQL data exception.
func (r *Descriptor) subtractRange(a, b rangeDatum) (left, right rangeDatum, count int, err error) {
	if !r.rangeOverlaps(a, b) {
		return a, rangeDatum{}, 1, nil
	}
	before, after := r.compareRangeEndpoint(a, true, b, true) < 0, r.compareRangeEndpoint(a, false, b, false) > 0
	if before {
		x := a
		x.upper = b.lower
		x.flags = x.flags &^ (rangeUpperInclusive | rangeUpperUnbounded)
		if b.flags&rangeLowerInclusive == 0 {
			x.flags |= rangeUpperInclusive
		}
		x, err = r.canonicalRange(x, false)
		if err != nil {
			return left, right, 0, err
		}
		if x.flags&rangeEmpty == 0 {
			left = x
			count = 1
		}
	}
	if after {
		x := a
		x.lower = b.upper
		x.flags = x.flags &^ (rangeLowerInclusive | rangeLowerUnbounded)
		if b.flags&rangeUpperInclusive == 0 {
			x.flags |= rangeLowerInclusive
		}
		x, err = r.canonicalRange(x, false)
		if err != nil {
			return left, right, 0, err
		}
		if x.flags&rangeEmpty == 0 {
			if count == 0 {
				left = x
			} else {
				right = x
			}
			count++
		}
	}
	return
}
func (p RangeOperatorPlan) evaluateSet(ctx context.Context, a, b Value) (Value, error) {
	r := p.rangeType
	if p.leftKind == rangeSingleOperand {
		x, y := a.rangeDatum, b.rangeDatum
		xe, ye := x.flags&rangeEmpty != 0, y.flags&rangeEmpty != 0
		switch p.operation {
		case RangeUnion:
			if xe {
				x = y
			} else if !ye {
				if !r.rangeOverlaps(x, y) && !r.rangeBoundsAdjacent(x, y) && !r.rangeBoundsAdjacent(y, x) {
					return Value{}, failure("22000", "result of range union would not be contiguous")
				}
				x = r.unionRanges(x, y)
			}
		case RangeIntersection:
			if xe || ye {
				x = rangeDatum{flags: rangeEmpty}
			} else {
				x = r.intersectRangeDatums(x, y)
			}
		case RangeDifference:
			if !xe && !ye {
				lo, _, n, err := r.subtractRange(x, y)
				if err != nil {
					return Value{}, err
				}
				if n == 2 {
					return Value{}, failure("22000", "result of range difference would not be contiguous")
				}
				if n == 0 {
					x = rangeDatum{flags: rangeEmpty}
				} else {
					x = lo
				}
			}
		}
		return Value{typ: p.result, rangeDatum: x}, nil
	}
	aa, bb := a.multirange.members, b.multirange.members
	if len(aa) == 0 {
		if p.operation == RangeUnion {
			return b, nil
		}
		return a, nil
	}
	if len(bb) == 0 {
		if p.operation == RangeIntersection {
			return b, nil
		}
		return a, nil
	}
	// Result buffers are execution-owned; no operation writes either input slice.
	var out []rangeDatum
	switch p.operation {
	case RangeUnion:
		out = make([]rangeDatum, 0, len(aa)+len(bb))
		i, j := 0, 0
		for i < len(aa) || j < len(bb) {
			if (i+j)&255 == 0 {
				if err := ctx.Err(); err != nil {
					return Value{}, err
				}
			}
			var x rangeDatum
			if j == len(bb) || i < len(aa) && r.compareRanges(aa[i], bb[j]) < 0 {
				x = aa[i]
				i++
			} else {
				x = bb[j]
				j++
			}
			if len(out) == 0 {
				out = append(out, x)
				continue
			}
			last := &out[len(out)-1]
			if r.rangeOverlaps(*last, x) || r.rangeBoundsAdjacent(*last, x) {
				*last = r.unionRanges(*last, x)
			} else {
				out = append(out, x)
			}
		}
	case RangeIntersection:
		i, j, steps := 0, 0, 0
		for i < len(aa) && j < len(bb) {
			if steps&255 == 0 {
				if err := ctx.Err(); err != nil {
					return Value{}, err
				}
			}
			steps++
			x := r.intersectRangeDatums(aa[i], bb[j])
			if x.flags&rangeEmpty == 0 {
				out = appendOperatorRange(out, x, len(aa)+len(bb))
			}
			cmp := r.compareRangeEndpoint(aa[i], false, bb[j], false)
			if cmp <= 0 {
				i++
			}
			if cmp >= 0 {
				j++
			}
		}
	case RangeDifference:
		j := 0
		steps := 0
		for index, a := range aa {
			if index&255 == 0 {
				if err := ctx.Err(); err != nil {
					return Value{}, err
				}
			}
			remaining := a
			alive := true
			for j < len(bb) && r.compareRangeEndpoint(bb[j], false, remaining, true) < 0 {
				j++
				steps++
				if steps&255 == 0 {
					if err := ctx.Err(); err != nil {
						return Value{}, err
					}
				}
			}
			for alive && j < len(bb) && r.compareRangeEndpoint(bb[j], true, remaining, false) <= 0 {
				if steps&255 == 0 {
					if err := ctx.Err(); err != nil {
						return Value{}, err
					}
				}
				steps++
				lo, hi, n, err := r.subtractRange(remaining, bb[j])
				if err != nil {
					return Value{}, err
				}
				switch n {
				case 0:
					alive = false
				case 2:
					out = appendOperatorRange(out, lo, len(aa)+len(bb))
					remaining = hi
				case 1:
					if r.compareRangeEndpoint(lo, true, remaining, true) == 0 {
						out = appendOperatorRange(out, lo, len(aa)+len(bb))
						alive = false
					} else {
						remaining = lo
					}
				}
				// A cutter reaching beyond this member may also affect the next member.
				if r.compareRangeEndpoint(bb[j], false, a, false) >= 0 {
					break
				}
				j++
			}
			if alive {
				out = appendOperatorRange(out, remaining, len(aa)+len(bb))
			}
		}
	}
	return Value{typ: p.result, multirange: &multirangePayload{out}}, ctx.Err()
}

// Delay the sole writable member buffer until an operation actually emits a
// member. Empty intersections/differences need no backing allocation.
func appendOperatorRange(out []rangeDatum, member rangeDatum, capacity int) []rangeDatum {
	if out == nil {
		out = make([]rangeDatum, 0, capacity)
	}
	return append(out, member)
}
