package sqltype

import (
	"context"
	"fmt"
	"math"
)

// EarthPointPlan measures (longitude,latitude) POINTs in statute miles. It has
// no dependency on the installed earth domain or its meter-valued radius.
type EarthPointPlan struct {
	input, result *Descriptor
}

func BindEarthPoint(result, left, right Usage) (EarthPointPlan, error) {
	if left.Type == nil || left.Type.id != Point || left != left.Type.Usage() || right != left || result.Type == nil || result.Type.id != Float8 || result != result.Type.Usage() {
		return EarthPointPlan{}, fmt.Errorf("invalid earth point signature")
	}
	if result.Type != left.Type.pointFloat {
		return EarthPointPlan{}, fmt.Errorf("foreign earth point result authority")
	}
	return EarthPointPlan{left.Type, result.Type}, nil
}

func (p EarthPointPlan) Evaluate(ctx context.Context, values [2]Value) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if p.input == nil || p.result == nil {
		return Value{}, fmt.Errorf("invalid earth point plan")
	}
	for _, v := range values {
		if v.typ != p.input {
			return Value{}, fmt.Errorf("invalid earth point operand")
		}
	}
	if values[0].null || values[1].null {
		return Null(p.result), nil
	}
	long1, lat1 := pointCoordinates(values[0])
	long2, lat2 := pointCoordinates(values[1])
	// Retain the released C evaluation order and raw nonfinite propagation.
	long1, lat1 = (long1/360)*(2*math.Pi), (lat1/360)*(2*math.Pi)
	long2, lat2 = (long2/360)*(2*math.Pi), (lat2/360)*(2*math.Pi)
	longdiff := math.Abs(long1 - long2)
	if longdiff > math.Pi {
		longdiff = 2*math.Pi - longdiff
	}
	latdiff := math.Abs(lat1 - lat2)
	sinlat, sinlong := math.Sin(latdiff/2), math.Sin(longdiff/2)
	root := math.Sqrt(sinlat*sinlat + math.Cos(lat1)*math.Cos(lat2)*sinlong*sinlong)
	if root > 1 {
		root = 1
	}
	return Float(p.result, 2*3958.747716*math.Asin(root))
}
