package sqltype

import (
	"context"
	"fmt"
)

func relationValuePosition(ctx context.Context, a, b Value, name string) (bool, error) {
	if a.typ.id == Box {
		// Component writes preserve raw high/low corners. Do not normalize or
		// compute area: positions compare only their selected endpoints.
		x, y := a.geometry.coordinates, b.geometry.coordinates
		switch name {
		case "<<":
			return x[0]+1e-6 < y[2], nil
		case ">>":
			return x[2] > y[0]+1e-6, nil
		case "&<":
			return x[0] <= y[0]+1e-6, nil
		case "&>":
			return x[2]+1e-6 >= y[2], nil
		case "<<|":
			return x[1]+1e-6 < y[3], nil
		case "|>>":
			return x[3] > y[1]+1e-6, nil
		case "&<|":
			return x[1] <= y[1]+1e-6, nil
		case "|&>":
			return x[3]+1e-6 >= y[3], nil
		case "<^":
			return x[1] <= y[3]+1e-6, nil
		case ">^":
			return x[3]+1e-6 >= y[1], nil
		}
	}
	if a.typ.id == Polygon {
		x, err := relationPolygonBounds(ctx, a.polygon.points)
		if err != nil {
			return false, err
		}
		y, err := relationPolygonBounds(ctx, b.polygon.points)
		if err != nil {
			return false, err
		}
		// Released polygon positions use raw IEEE comparisons, unlike BOX.
		// In particular an inclusive NaN comparison is not !strict.
		switch name {
		case "<<":
			return x[0] < y[2], nil
		case ">>":
			return x[2] > y[0], nil
		case "&<":
			return x[0] <= y[0], nil
		case "&>":
			return x[2] >= y[2], nil
		case "<<|":
			return x[1] < y[3], nil
		case "|>>":
			return x[3] > y[1], nil
		case "&<|":
			return x[1] <= y[1], nil
		case "|&>":
			return x[3] >= y[3], nil
		}
	}
	return false, fmt.Errorf("invalid geometry position predicate")
}
