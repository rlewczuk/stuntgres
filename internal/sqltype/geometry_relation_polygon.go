package sqltype

import "context"

func relationPolygonPoint(p polygonPoint) relationPoint { return relationPoint{p.x, p.y} }
func relationPolygonEdge(p []polygonPoint, i int) relationSegment {
	previous := i - 1
	if i == 0 {
		previous = len(p) - 1
	}
	return relationSegment{relationPolygonPoint(p[previous]), relationPolygonPoint(p[i])}
}
func relationPolygonBounds(ctx context.Context, p []polygonPoint) ([4]float64, error) {
	b := [4]float64{p[0].x, p[0].y, p[0].x, p[0].y}
	for _, q := range p[1:] {
		if err := ctx.Err(); err != nil {
			return b, err
		}
		b[0], b[1] = relationMax(q.x, b[0]), relationMax(q.y, b[1])
		b[2], b[3] = relationMin(q.x, b[2]), relationMin(q.y, b[3])
	}
	return b, nil
}
func relationPolygonOverlap(ctx context.Context, p, q []polygonPoint) (bool, error) {
	a, err := relationPolygonBounds(ctx, p)
	if err != nil {
		return false, err
	}
	b, err := relationPolygonBounds(ctx, q)
	if err != nil {
		return false, err
	}
	if !relationBoxOverlap(a, b) {
		return false, nil
	}
	for i := range p {
		s := relationPolygonEdge(p, i)
		for j := range q {
			if err := ctx.Err(); err != nil {
				return false, err
			}
			_, hit, err := relationSegmentIntersection(s, relationPolygonEdge(q, j))
			if err != nil || hit {
				return hit, err
			}
		}
	}
	hit, err := polygonContains(ctx, q, p[0].x, p[0].y)
	if err != nil || hit {
		return hit, err
	}
	return polygonContains(ctx, p, q[0].x, q[0].y)
}
func relationPolygonPointDistance(ctx context.Context, p []polygonPoint, q relationPoint) (float64, error) {
	hit, err := polygonContains(ctx, p, q[0], q[1])
	if err != nil || hit {
		return 0, err
	}
	// dist_ppoly_internal alone anchors the closing candidate at the first
	// vertex. Reversal changes epsilon-horizontal/vertical line promotion.
	closing := relationSegment{relationPolygonPoint(p[0]), relationPolygonPoint(p[len(p)-1])}
	_, best, err := relationSegmentPoint(closing, q)
	if err != nil {
		return 0, err
	}
	// Closing edge first; subsequent edges follow the released traversal.
	for i := 1; i < len(p); i++ {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		_, d, err := relationSegmentPoint(relationPolygonEdge(p, i), q)
		if err != nil {
			return 0, err
		}
		if relationLess(d, best) {
			best = d
		}
	}
	return best, nil
}
func relationPolygonDistance(ctx context.Context, p, q []polygonPoint) (float64, error) {
	hit, err := relationPolygonOverlap(ctx, p, q)
	if err != nil || hit {
		return 0, err
	}
	best, have := 0.0, false
	for i := range p {
		s := relationPolygonEdge(p, i)
		for j := range q {
			if err := ctx.Err(); err != nil {
				return 0, err
			}
			_, d, err := relationSegmentClosest(s, relationPolygonEdge(q, j))
			if err != nil {
				return 0, err
			}
			if !have || relationLess(d, best) {
				best, have = d, true
			}
		}
	}
	return best, nil
}

// REL_18_6 touched_lseg_inside_poly and lseg_inside_poly. The monotonically
// increasing start bounds recursion by the polygon's edges. Each branch checks
// cancellation, and no converted polygons or per-edge coordinate slices exist.
func relationTouchedInside(ctx context.Context, a, b relationPoint, s relationSegment, p []polygonPoint, start int) (bool, error) {
	t := relationSegment{a, b}
	same := func(q relationPoint) bool { return pointSame(a[0], a[1], q[0], q[1]) }
	if same(s[0]) {
		on, err := relationOnSegment(t, s[1])
		if err != nil {
			return false, err
		}
		if on {
			return relationSegmentInside(ctx, b, s[1], p, start)
		}
	} else if same(s[1]) {
		on, err := relationOnSegment(t, s[0])
		if err != nil {
			return false, err
		}
		if on {
			return relationSegmentInside(ctx, b, s[0], p, start)
		}
	} else {
		on, err := relationOnSegment(t, s[0])
		if err != nil {
			return false, err
		}
		if on {
			return relationSegmentInside(ctx, b, s[0], p, start)
		}
		on, err = relationOnSegment(t, s[1])
		if err != nil {
			return false, err
		}
		if on {
			return relationSegmentInside(ctx, b, s[1], p, start)
		}
	}
	return true, nil
}
func relationSegmentInside(ctx context.Context, a, b relationPoint, p []polygonPoint, start int) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	t := relationSegment{a, b}
	intersection := false
	for i := start; i < len(p); i++ {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		s := relationPolygonEdge(p, i)
		onA, err := relationOnSegment(s, a)
		if err != nil {
			return false, err
		}
		if onA {
			onB, err := relationOnSegment(s, b)
			if err != nil {
				return false, err
			}
			if onB {
				return true, nil
			}
			inside, err := relationTouchedInside(ctx, a, b, s, p, i+1)
			if err != nil || !inside {
				return inside, err
			}
		} else {
			onB, err := relationOnSegment(s, b)
			if err != nil {
				return false, err
			}
			if onB {
				inside, err := relationTouchedInside(ctx, b, a, s, p, i+1)
				if err != nil || !inside {
					return inside, err
				}
			} else {
				cross, hit, err := relationSegmentIntersection(t, s)
				if err != nil {
					return false, err
				}
				if hit {
					intersection = true
					inside, err := relationSegmentInside(ctx, a, cross, p, i+1)
					if err != nil || !inside {
						return inside, err
					}
					inside, err = relationSegmentInside(ctx, b, cross, p, i+1)
					if err != nil || !inside {
						return inside, err
					}
				}
			}
		}
	}
	if intersection {
		return true, nil
	}
	center, err := geometryMidpoint([4]float64{a[0], a[1], b[0], b[1]})
	if err != nil {
		return false, err
	}
	return polygonContains(ctx, p, center[0], center[1])
}
func relationPolygonContains(ctx context.Context, p, q []polygonPoint) (bool, error) {
	a, err := relationPolygonBounds(ctx, p)
	if err != nil {
		return false, err
	}
	b, err := relationPolygonBounds(ctx, q)
	if err != nil {
		return false, err
	}
	if !relationBoxContains(a, b) {
		return false, nil
	}
	for i := range q {
		s := relationPolygonEdge(q, i)
		inside, err := relationSegmentInside(ctx, s[0], s[1], p, 0)
		if err != nil || !inside {
			return inside, err
		}
	}
	return true, nil
}
