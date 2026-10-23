package sqltype

import "math"

// These stack-only coordinates mirror REL_18_6 geo_ops.c. In particular,
// segment promotion accepts coincident endpoints and is not the public LINE
// constructor. Arithmetic order and candidate order are observable contracts.
type relationPoint = [2]float64
type relationSegment = [2]relationPoint
type relationLine = [3]float64

type relationArithmetic struct{ err error }

func (a *relationArithmetic) binary(op ArithmeticOperator, x, y float64) float64 {
	if a.err != nil {
		return 0
	}
	var n float64
	n, a.err = checkedFloatingBinary(op, Float8, x, y)
	return n
}
func (a *relationArithmetic) add(x, y float64) float64 { return a.binary(ArithmeticAdd, x, y) }
func (a *relationArithmetic) sub(x, y float64) float64 { return a.binary(ArithmeticSubtract, x, y) }
func (a *relationArithmetic) mul(x, y float64) float64 { return a.binary(ArithmeticMultiply, x, y) }
func (a *relationArithmetic) div(x, y float64) float64 { return a.binary(ArithmeticDivide, x, y) }
func relationSlope(p, q relationPoint, inverse bool) (float64, error) {
	if circleFPeq(p[0], q[0]) {
		if inverse {
			return 0, nil
		}
		return math.Inf(1), nil
	}
	if circleFPeq(p[1], q[1]) {
		if inverse {
			return math.Inf(1), nil
		}
		return 0, nil
	}
	a := relationArithmetic{}
	if inverse {
		return a.div(a.sub(p[0], q[0]), a.sub(q[1], p[1])), a.err
	}
	return a.div(a.sub(p[1], q[1]), a.sub(p[0], q[0])), a.err
}
func relationConstructLine(p relationPoint, slope float64) (relationLine, error) {
	if math.IsInf(slope, 0) {
		return relationLine{-1, 0, p[0]}, nil
	}
	if slope == 0 {
		return relationLine{0, -1, p[1]}, nil
	}
	a := relationArithmetic{}
	c := a.sub(p[1], a.mul(slope, p[0]))
	if c == 0 {
		c = 0
	}
	return relationLine{slope, -1, c}, a.err
}
func relationSegmentLine(s relationSegment) (relationLine, error) {
	slope, err := relationSlope(s[0], s[1], false)
	if err != nil {
		return relationLine{}, err
	}
	return relationConstructLine(s[0], slope)
}
func relationLineSlope(l relationLine, inverse bool) (float64, error) {
	if circleFPeq(l[0], 0) {
		if inverse {
			return math.Inf(1), nil
		}
		return 0, nil
	}
	if circleFPeq(l[1], 0) {
		if inverse {
			return 0, nil
		}
		return math.Inf(1), nil
	}
	a := relationArithmetic{}
	if inverse {
		return a.div(l[1], l[0]), a.err
	}
	return a.div(l[0], -l[1]), a.err
}
func relationLineIntersection(l, r relationLine) (relationPoint, bool, error) {
	a := relationArithmetic{}
	if circleFPeq(l[1], 0) {
		l, r = r, l
	}
	if circleFPeq(l[1], 0) {
		return relationPoint{}, false, nil
	}
	parallel := circleFPeq(r[0], a.mul(l[0], a.div(r[1], l[1])))
	if a.err != nil || parallel {
		return relationPoint{}, false, a.err
	}
	x := a.div(a.sub(a.mul(l[1], r[2]), a.mul(r[1], l[2])), a.sub(a.mul(l[0], r[1]), a.mul(r[0], l[1])))
	y := a.div(-a.add(a.mul(l[0], x), l[2]), l[1])
	if x == 0 {
		x = 0
	}
	if y == 0 {
		y = 0
	}
	return relationPoint{x, y}, true, a.err
}
func relationDistance(p, q relationPoint) (float64, error) {
	return geometryDistance(p[0], p[1], q[0], q[1])
}
func relationOnSegment(s relationSegment, p relationPoint) (bool, error) {
	x, err := relationDistance(p, s[0])
	if err != nil {
		return false, err
	}
	y, err := relationDistance(p, s[1])
	if err != nil {
		return false, err
	}
	d, err := relationDistance(s[0], s[1])
	if err != nil {
		return false, err
	}
	// lseg_contain_point uses a plain sum, unlike on_ppath's float8_pl.
	return circleFPeq(x+y, d), nil
}
func relationOnLine(l relationLine, p relationPoint) (bool, error) {
	a := relationArithmetic{}
	n := a.add(a.add(a.mul(l[0], p[0]), a.mul(l[1], p[1])), l[2])
	return circleFPeq(n, 0), a.err
}
func relationSegmentLineIntersection(s relationSegment, l relationLine) (relationPoint, bool, error) {
	sl, err := relationSegmentLine(s)
	if err != nil {
		return relationPoint{}, false, err
	}
	p, ok, err := relationLineIntersection(sl, l)
	if err != nil || !ok {
		return p, ok, err
	}
	ok, err = relationOnSegment(s, p)
	if err != nil || !ok {
		return p, ok, err
	}
	for _, q := range s {
		if pointSame(p[0], p[1], q[0], q[1]) {
			return q, true, nil
		}
	}
	return p, true, nil
}
func relationSegmentIntersection(l, r relationSegment) (relationPoint, bool, error) {
	line, err := relationSegmentLine(r)
	if err != nil {
		return relationPoint{}, false, err
	}
	p, ok, err := relationSegmentLineIntersection(l, line)
	if err != nil || !ok {
		return p, ok, err
	}
	ok, err = relationOnSegment(r, p)
	return p, ok, err
}
func relationLinePoint(l relationLine, p relationPoint) (relationPoint, float64, error) {
	slope, err := relationLineSlope(l, true)
	if err != nil {
		return p, 0, err
	}
	perp, err := relationConstructLine(p, slope)
	if err != nil {
		return p, 0, err
	}
	q, ok, err := relationLineIntersection(perp, l)
	if err != nil {
		return p, 0, err
	}
	if !ok {
		return p, math.NaN(), nil
	}
	d, err := relationDistance(q, p)
	return q, d, err
}
func relationSegmentToLine(s relationSegment, l relationLine) (relationPoint, float64, error) {
	p, ok, err := relationSegmentLineIntersection(s, l)
	if err != nil || ok {
		return p, 0, err
	}
	_, d1, err := relationLinePoint(l, s[0])
	if err != nil {
		return p, 0, err
	}
	_, d2, err := relationLinePoint(l, s[1])
	if err != nil {
		return p, 0, err
	}
	if d1 < d2 {
		return s[0], d1, nil
	}
	return s[1], d2, nil
}
func relationSegmentPoint(s relationSegment, p relationPoint) (relationPoint, float64, error) {
	slope, err := relationSlope(s[0], s[1], true)
	if err != nil {
		return p, 0, err
	}
	perp, err := relationConstructLine(p, slope)
	if err != nil {
		return p, 0, err
	}
	q, _, err := relationSegmentToLine(s, perp)
	if err != nil {
		return q, 0, err
	}
	d, err := relationDistance(q, p)
	return q, d, err
}

// PostgreSQL float8_lt orders NaN after every non-NaN value.
func relationLess(a, b float64) bool { return !math.IsNaN(a) && (math.IsNaN(b) || a < b) }
func relationMin(a, b float64) float64 {
	if relationLess(a, b) {
		return a
	}
	return b
}
func relationMax(a, b float64) float64 {
	if relationLess(a, b) {
		return b
	}
	return a
}
func relationSegmentClosest(on, to relationSegment) (relationPoint, float64, error) {
	p, ok, err := relationSegmentIntersection(on, to)
	if err != nil || ok {
		return p, 0, err
	}
	p, d, err := relationSegmentPoint(on, to[0])
	if err != nil {
		return p, d, err
	}
	q, n, err := relationSegmentPoint(on, to[1])
	if err != nil {
		return p, d, err
	}
	if relationLess(n, d) {
		p, d = q, n
	}
	for _, q := range on {
		_, n, err = relationSegmentPoint(to, q)
		if err != nil {
			return p, d, err
		}
		if relationLess(n, d) {
			p, d = q, n
		}
	}
	return p, d, nil
}
func relationInBox(b [4]float64, p relationPoint) bool {
	return b[0] >= p[0] && b[2] <= p[0] && b[1] >= p[1] && b[3] <= p[1]
}
func relationBoxOverlap(a, b [4]float64) bool {
	return a[0]+1e-6 >= b[2] && b[0]+1e-6 >= a[2] && a[1]+1e-6 >= b[3] && b[1]+1e-6 >= a[3]
}
func relationBoxContains(a, b [4]float64) bool {
	return a[0]+1e-6 >= b[0] && a[1]+1e-6 >= b[1] && a[2] <= b[2]+1e-6 && a[3] <= b[3]+1e-6
}
func relationBoxEdges(b [4]float64) [4]relationSegment {
	low, high := relationPoint{b[2], b[3]}, relationPoint{b[0], b[1]}
	return [4]relationSegment{{low, {b[2], b[1]}}, {high, {b[2], b[1]}}, {low, {b[0], b[3]}}, {high, {b[0], b[3]}}}
}
func relationSegmentBounds(s relationSegment) [4]float64 {
	return [4]float64{relationMax(s[0][0], s[1][0]), relationMax(s[0][1], s[1][1]), relationMin(s[0][0], s[1][0]), relationMin(s[0][1], s[1][1])}
}
func relationBoxSegmentIntersects(b [4]float64, s relationSegment) (bool, error) {
	if !relationBoxOverlap(b, relationSegmentBounds(s)) {
		return false, nil
	}
	return relationBoxSegmentHit(b, s)
}

// The caller has already admitted bounding-box overlap.
func relationBoxSegmentHit(b [4]float64, s relationSegment) (bool, error) {
	if relationInBox(b, s[0]) || relationInBox(b, s[1]) {
		return true, nil
	}
	for _, edge := range relationBoxEdges(b) {
		_, ok, err := relationSegmentIntersection(edge, s)
		if err != nil || ok {
			return ok, err
		}
	}
	return false, nil
}
func relationBoxPoint(b [4]float64, p relationPoint) (relationPoint, float64, error) {
	if relationInBox(b, p) {
		return p, 0, nil
	}
	var best relationPoint
	var distance float64
	for i, edge := range relationBoxEdges(b) {
		q, d, err := relationSegmentPoint(edge, p)
		if err != nil {
			return q, d, err
		}
		if i == 0 || relationLess(d, distance) {
			best, distance = q, d
		}
	}
	return best, distance, nil
}
func relationBoxSegment(b [4]float64, s relationSegment, closest bool) (relationPoint, float64, error) {
	// The released closest-point path computes this representative after bbox
	// rejection but before testing endpoints/edges, even when no hit follows.
	var representative relationPoint
	if relationBoxOverlap(b, relationSegmentBounds(s)) {
		if closest {
			center, err := geometryMidpoint(b)
			if err != nil {
				return representative, 0, err
			}
			representative, _, err = relationSegmentPoint(s, center)
			if err != nil {
				return representative, 0, err
			}
		}
		ok, err := relationBoxSegmentHit(b, s)
		if err != nil || ok {
			return representative, 0, err
		}
	}
	var best relationPoint
	var distance float64
	for i, edge := range relationBoxEdges(b) {
		q, d, err := relationSegmentClosest(edge, s)
		if err != nil {
			return q, d, err
		}
		if i == 0 || relationLess(d, distance) {
			best, distance = q, d
		}
	}
	return best, distance, nil
}
func relationLineDistance(l, r relationLine) (float64, error) {
	_, ok, err := relationLineIntersection(l, r)
	if err != nil || ok {
		return 0, err
	}
	a := relationArithmetic{}
	ratio := 1.0
	if !circleFPeq(l[0], 0) && !math.IsNaN(l[0]) && !circleFPeq(r[0], 0) && !math.IsNaN(r[0]) {
		ratio = a.div(l[0], r[0])
	} else if !circleFPeq(l[1], 0) && !math.IsNaN(l[1]) && !circleFPeq(r[1], 0) && !math.IsNaN(r[1]) {
		ratio = a.div(l[1], r[1])
	}
	numerator := math.Abs(a.sub(l[2], a.mul(ratio, r[2])))
	if a.err != nil {
		return 0, a.err
	}
	denominator, err := geometryDistance(l[0], l[1], 0, 0)
	if err != nil {
		return 0, err
	}
	n := a.div(numerator, denominator)
	return n, a.err
}
