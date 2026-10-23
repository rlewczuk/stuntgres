package sqltype

import "context"

func relationPathEdge(p *pathPayload, i int) (relationSegment, bool) {
	previous := i - 1
	if i == 0 {
		if !p.closed {
			return relationSegment{}, false
		}
		previous = len(p.points) - 1
	}
	return relationSegment{p.points[previous], p.points[i]}, true
}
func relationPathBounds(ctx context.Context, p *pathPayload) ([4]float64, error) {
	b := [4]float64{p.points[0][0], p.points[0][1], p.points[0][0], p.points[0][1]}
	for _, q := range p.points[1:] {
		if err := ctx.Err(); err != nil {
			return b, err
		}
		b[0], b[1] = relationMax(q[0], b[0]), relationMax(q[1], b[1])
		b[2], b[3] = relationMin(q[0], b[2]), relationMin(q[1], b[3])
	}
	return b, nil
}
func relationPaths(ctx context.Context, p, q *pathPayload, intersection bool) (float64, bool, error) {
	if intersection {
		a, err := relationPathBounds(ctx, p)
		if err != nil {
			return 0, false, err
		}
		b, err := relationPathBounds(ctx, q)
		if err != nil {
			return 0, false, err
		}
		if !relationBoxOverlap(a, b) {
			return 0, false, nil
		}
	}
	best, have := 0.0, false
	for i := range p.points {
		s, ok := relationPathEdge(p, i)
		if !ok {
			continue
		}
		for j := range q.points {
			if err := ctx.Err(); err != nil {
				return 0, false, err
			}
			t, ok := relationPathEdge(q, j)
			if !ok {
				continue
			}
			if intersection {
				_, hit, err := relationSegmentIntersection(s, t)
				if err != nil || hit {
					return 0, hit, err
				}
				continue
			}
			_, d, err := relationSegmentClosest(s, t)
			if err != nil {
				return 0, false, err
			}
			if !have || relationLess(d, best) {
				best, have = d, true
			}
		}
	}
	return best, have, nil
}
func relationPathPointDistance(ctx context.Context, p *pathPayload, q relationPoint) (float64, error) {
	best, have := 0.0, false
	for i := range p.points {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		s, ok := relationPathEdge(p, i)
		if !ok {
			continue
		}
		_, d, err := relationSegmentPoint(s, q)
		if err != nil {
			return 0, err
		}
		if !have || relationLess(d, best) {
			best, have = d, true
		}
	}
	return best, nil
}
func relationPathContains(ctx context.Context, p *pathPayload, q relationPoint) (bool, error) {
	if p.closed {
		// Use the existing ray-crossing kernel without allocating a converted polygon.
		x0, err := checkedFloatingBinary(ArithmeticSubtract, Float8, p.points[0][0], q[0])
		if err != nil {
			return false, err
		}
		y0, err := checkedFloatingBinary(ArithmeticSubtract, Float8, p.points[0][1], q[1])
		if err != nil {
			return false, err
		}
		px, py, total := x0, y0, int64(0)
		for _, v := range p.points[1:] {
			if err := ctx.Err(); err != nil {
				return false, err
			}
			x, err := checkedFloatingBinary(ArithmeticSubtract, Float8, v[0], q[0])
			if err != nil {
				return false, err
			}
			y, err := checkedFloatingBinary(ArithmeticSubtract, Float8, v[1], q[1])
			if err != nil {
				return false, err
			}
			cross, boundary, err := polygonCrossing(x, y, px, py)
			if err != nil || boundary {
				return boundary, err
			}
			total += int64(cross)
			px, py = x, y
		}
		cross, boundary, err := polygonCrossing(x0, y0, px, py)
		return boundary || total+int64(cross) != 0, err
	}
	a, err := relationDistance(q, p.points[0])
	if err != nil {
		return false, err
	}
	for i := 1; i < len(p.points); i++ {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		b, err := relationDistance(q, p.points[i])
		if err != nil {
			return false, err
		}
		sum, err := checkedFloatingBinary(ArithmeticAdd, Float8, a, b)
		if err != nil {
			return false, err
		}
		d, err := relationDistance(p.points[i-1], p.points[i])
		if err != nil {
			return false, err
		}
		if circleFPeq(sum, d) {
			return true, nil
		}
		a = b
	}
	return false, nil
}
