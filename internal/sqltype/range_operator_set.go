package sqltype

import "context"

func rangeFromBounds(lower, upper rangeDatum) rangeDatum {
	return rangeDatum{lower: lower.lower, upper: upper.upper, flags: lower.flags&(rangeLowerInclusive|rangeLowerUnbounded) | upper.flags&(rangeUpperInclusive|rangeUpperUnbounded)}
}
func (r *Descriptor) unionRanges(ctx context.Context, a, b rangeDatum) (rangeDatum, error) {
	lo, hi := a, a
	cmp, err := r.compareRangeEndpoint(ctx, a, true, b, true)
	if err != nil {
		return rangeDatum{}, err
	}
	if cmp >= 0 {
		lo = b
	}
	cmp, err = r.compareRangeEndpoint(ctx, a, false, b, false)
	if err != nil {
		return rangeDatum{}, err
	}
	if cmp <= 0 {
		hi = b
	}
	return rangeFromBounds(lo, hi), nil
}

func (r *Descriptor) connectedRanges(ctx context.Context, a, b rangeDatum) (bool, error) {
	yes, err := r.rangeOverlaps(ctx, a, b)
	if yes || err != nil {
		return yes, err
	}
	yes, err = r.rangeBoundsAdjacent(ctx, a, b)
	if yes || err != nil {
		return yes, err
	}
	return r.rangeBoundsAdjacent(ctx, b, a)
}

// Subtract one member into at most two canonical survivors. The caller decides
// whether two pieces are representable (multirange) or an SQL data exception.
func (r *Descriptor) subtractRange(ctx context.Context, a, b rangeDatum) (left, right rangeDatum, count int, err error) {
	overlaps, err := r.rangeOverlaps(ctx, a, b)
	if err != nil {
		return left, right, 0, err
	}
	if !overlaps {
		return a, rangeDatum{}, 1, nil
	}
	lo, err := r.compareRangeEndpoint(ctx, a, true, b, true)
	if err != nil {
		return left, right, 0, err
	}
	hi, err := r.compareRangeEndpoint(ctx, a, false, b, false)
	if err != nil {
		return left, right, 0, err
	}
	before, after := lo < 0, hi > 0
	if before {
		x := a
		x.upper = b.lower
		x.flags = x.flags &^ (rangeUpperInclusive | rangeUpperUnbounded)
		if b.flags&rangeLowerInclusive == 0 {
			x.flags |= rangeUpperInclusive
		}
		x, err = r.canonicalRange(ctx, x, false)
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
		x, err = r.canonicalRange(ctx, x, false)
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
				connected, err := r.connectedRanges(ctx, x, y)
				if err != nil {
					return Value{}, err
				}
				if !connected {
					return Value{}, failure("22000", "result of range union would not be contiguous")
				}
				x, err = r.unionRanges(ctx, x, y)
				if err != nil {
					return Value{}, err
				}
			}
		case RangeIntersection:
			if xe || ye {
				x = rangeDatum{flags: rangeEmpty}
			} else {
				var err error
				x, err = r.intersectRangeDatums(ctx, x, y)
				if err != nil {
					return Value{}, err
				}
			}
		case RangeDifference:
			if !xe && !ye {
				lo, _, n, err := r.subtractRange(ctx, x, y)
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
			left := j == len(bb)
			if i < len(aa) && j < len(bb) {
				cmp, err := r.compareRanges(ctx, aa[i], bb[j])
				if err != nil {
					return Value{}, err
				}
				left = cmp < 0
			}
			if left {
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
			connected, err := r.connectedRanges(ctx, *last, x)
			if err != nil {
				return Value{}, err
			}
			if connected {
				*last, err = r.unionRanges(ctx, *last, x)
				if err != nil {
					return Value{}, err
				}
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
			x, err := r.intersectRangeDatums(ctx, aa[i], bb[j])
			if err != nil {
				return Value{}, err
			}
			if x.flags&rangeEmpty == 0 {
				out = appendOperatorRange(out, x, len(aa)+len(bb))
			}
			cmp, err := r.compareRangeEndpoint(ctx, aa[i], false, bb[j], false)
			if err != nil {
				return Value{}, err
			}
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
			for j < len(bb) {
				cmp, err := r.compareRangeEndpoint(ctx, bb[j], false, remaining, true)
				if err != nil {
					return Value{}, err
				}
				if cmp >= 0 {
					break
				}
				j++
				steps++
				if steps&255 == 0 {
					if err := ctx.Err(); err != nil {
						return Value{}, err
					}
				}
			}
			for alive && j < len(bb) {
				cmp, err := r.compareRangeEndpoint(ctx, bb[j], true, remaining, false)
				if err != nil {
					return Value{}, err
				}
				if cmp > 0 {
					break
				}
				if steps&255 == 0 {
					if err := ctx.Err(); err != nil {
						return Value{}, err
					}
				}
				steps++
				lo, hi, n, err := r.subtractRange(ctx, remaining, bb[j])
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
					cmp, err := r.compareRangeEndpoint(ctx, lo, true, remaining, true)
					if err != nil {
						return Value{}, err
					}
					if cmp == 0 {
						out = appendOperatorRange(out, lo, len(aa)+len(bb))
						alive = false
					} else {
						remaining = lo
					}
				}
				// A cutter reaching beyond this member may also affect the next member.
				cmp, err = r.compareRangeEndpoint(ctx, bb[j], false, a, false)
				if err != nil {
					return Value{}, err
				}
				if cmp >= 0 {
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
