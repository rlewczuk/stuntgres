package sqltype

import (
	"fmt"
)

func lsegPredicate(op ComparisonOperator, a, b Value) (bool, error) {
	if a.geometry == nil || b.geometry == nil {
		return false, fmt.Errorf("missing LSEG payload")
	}
	x, y := &a.geometry.coordinates, &b.geometry.coordinates
	if op == CompareEqual || op == CompareNotEqual {
		equal := pointSame(x[0], x[1], y[0], y[1]) && pointSame(x[2], x[3], y[2], y[3])
		if op == CompareNotEqual {
			equal = !equal
		}
		return equal, nil
	}
	left, err := geometryDistance(x[0], x[1], x[2], x[3])
	if err != nil {
		return false, err
	}
	right, err := geometryDistance(y[0], y[1], y[2], y[3])
	if err != nil {
		return false, err
	}
	switch op {
	case CompareLess:
		return left+1e-6 < right, nil
	case CompareLessEqual:
		return left <= right+1e-6, nil
	case CompareGreater:
		return left > right+1e-6, nil
	case CompareGreaterEqual:
		return left+1e-6 >= right, nil
	}
	return false, failure("42883", "incompatible comparison operands")
}
