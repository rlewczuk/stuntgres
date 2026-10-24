package sqltype

import "context"

// Keep every FLOAT8 intermediate checked and in the released expression grouping.
func geometryTransformPoint(op ArithmeticOperator, x, y, u, v float64) (float64, float64, error) {
	if op == ArithmeticAdd || op == ArithmeticSubtract {
		a, err := checkedFloatingBinary(op, Float8, x, u)
		if err != nil {
			return 0, 0, err
		}
		b, err := checkedFloatingBinary(op, Float8, y, v)
		return a, b, err
	}
	var denominator float64
	if op == ArithmeticDivide {
		u2, err := checkedFloatingBinary(ArithmeticMultiply, Float8, u, u)
		if err != nil {
			return 0, 0, err
		}
		v2, err := checkedFloatingBinary(ArithmeticMultiply, Float8, v, v)
		if err != nil {
			return 0, 0, err
		}
		denominator, err = checkedFloatingBinary(ArithmeticAdd, Float8, u2, v2)
		if err != nil {
			return 0, 0, err
		}
	}
	xu, err := checkedFloatingBinary(ArithmeticMultiply, Float8, x, u)
	if err != nil {
		return 0, 0, err
	}
	yv, err := checkedFloatingBinary(ArithmeticMultiply, Float8, y, v)
	if err != nil {
		return 0, 0, err
	}
	xop := ArithmeticSubtract
	if op == ArithmeticDivide {
		xop = ArithmeticAdd
	}
	a, err := checkedFloatingBinary(xop, Float8, xu, yv)
	if err != nil {
		return 0, 0, err
	}
	if op == ArithmeticDivide {
		a, err = checkedFloatingBinary(ArithmeticDivide, Float8, a, denominator)
		if err != nil {
			return 0, 0, err
		}
	}
	xv, err := checkedFloatingBinary(ArithmeticMultiply, Float8, x, v)
	if err != nil {
		return 0, 0, err
	}
	yu, err := checkedFloatingBinary(ArithmeticMultiply, Float8, y, u)
	if err != nil {
		return 0, 0, err
	}
	var b float64
	if op == ArithmeticDivide {
		b, err = checkedFloatingBinary(ArithmeticSubtract, Float8, yu, xv)
		if err == nil {
			b, err = checkedFloatingBinary(ArithmeticDivide, Float8, b, denominator)
		}
	} else {
		b, err = checkedFloatingBinary(ArithmeticAdd, Float8, xv, yu)
	}
	return a, b, err
}

// PostgreSQL PATH has a 16-byte header, 16-byte points and signed-int32 byte size.
func geometryConcatCount(left, right int) (int, error) {
	const maxPoints = (0x7fffffff - 16) / 16
	if left < 0 || right < 0 || left > maxPoints || right > maxPoints-left {
		return 0, failure("54000", "too many points requested")
	}
	return left + right, nil
}

func (p Arithmetic) geometryBinary(ctx context.Context, a, b Value) (Value, error) {
	if p.right.id == Path {
		if a.path.closed || b.path.closed {
			return Null(p.result), nil
		}
		n, err := geometryConcatCount(len(a.path.points), len(b.path.points))
		if err != nil {
			return Value{}, err
		}
		points := make([][2]float64, n)
		for i := range points {
			if err := ctx.Err(); err != nil {
				return Value{}, err
			}
			if i < len(a.path.points) {
				points[i] = a.path.points[i]
			} else {
				points[i] = b.path.points[i-len(a.path.points)]
			}
		}
		return Value{typ: p.result, path: &pathPayload{points: points}}, nil
	}
	u, v := pointCoordinates(b)
	switch p.left.id {
	case Point:
		x, y := pointCoordinates(a)
		x, y, err := geometryTransformPoint(p.operator, x, y, u, v)
		if err != nil {
			return Value{}, err
		}
		return pointValue(p.result, x, y), nil
	case Box:
		coords := a.geometry.coordinates
		for i := range 2 {
			x, y, err := geometryTransformPoint(p.operator, coords[2*i], coords[2*i+1], u, v)
			if err != nil {
				return Value{}, err
			}
			coords[2*i], coords[2*i+1] = x, y
		}
		if p.operator == ArithmeticMultiply || p.operator == ArithmeticDivide {
			for i := range 2 {
				if compareFloats(coords[i], coords[i+2]) <= 0 {
					coords[i], coords[i+2] = coords[i+2], coords[i]
				}
			}
		}
		return Value{typ: p.result, geometry: &geometryPayload{coordinates: coords}}, nil
	case Path:
		points := make([][2]float64, len(a.path.points))
		for i, point := range a.path.points {
			if err := ctx.Err(); err != nil {
				return Value{}, err
			}
			x, y, err := geometryTransformPoint(p.operator, point[0], point[1], u, v)
			if err != nil {
				return Value{}, err
			}
			points[i] = [2]float64{x, y}
		}
		return Value{typ: p.result, path: &pathPayload{closed: a.path.closed, points: points}}, nil
	case Circle:
		center := a.circle
		x, y, err := geometryTransformPoint(p.operator, center.x, center.y, u, v)
		if err != nil {
			return Value{}, err
		}
		radius := center.radius
		if p.operator == ArithmeticMultiply || p.operator == ArithmeticDivide {
			norm, err := geometryHypot(u, v)
			if err != nil {
				return Value{}, err
			}
			radius, err = checkedFloatingBinary(p.operator, Float8, radius, norm)
			if err != nil {
				return Value{}, err
			}
		}
		return Value{typ: p.result, circle: circleDatum{x, y, radius}}, nil
	}
	panic("invalid bound geometric arithmetic")
}
