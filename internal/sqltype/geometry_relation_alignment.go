package sqltype

import "math"

func relationAlignment(left, right Value, name string) (bool, error) {
	if name == "?-" || name == "?|" {
		axis := 0
		if name == "?|" {
			axis = 1
		}
		if left.typ.id == Line {
			return math.Abs(left.geometry.coordinates[axis]) <= 1e-6, nil
		}
		s := relationValueSegment(left)
		// Horizontal segments compare Y; vertical segments compare X.
		axis = 1 - axis
		return circleFPeq(s[0][axis], s[1][axis]), nil
	}
	if left.typ.id == Lseg {
		l, r := relationValueSegment(left), relationValueSegment(right)
		a, err := relationSlope(l[0], l[1], false)
		if err != nil {
			return false, err
		}
		b, err := relationSlope(r[0], r[1], name == "?-|")
		if err != nil {
			return false, err
		}
		return circleFPeq(a, b), nil
	}
	l, r := relationValueLine(left), relationValueLine(right)
	if name == "?||" {
		_, found, err := relationLineIntersection(l, r)
		return !found, err
	}
	switch {
	case circleFPeq(l[0], 0):
		return circleFPeq(r[1], 0), nil
	case circleFPeq(r[0], 0):
		return circleFPeq(l[1], 0), nil
	case circleFPeq(l[1], 0):
		return circleFPeq(r[0], 0), nil
	case circleFPeq(r[1], 0):
		return circleFPeq(l[0], 0), nil
	}
	a := relationArithmetic{}
	n := a.div(a.mul(l[0], r[0]), a.mul(l[1], r[1]))
	return circleFPeq(n, -1), a.err
}
