package sqltype

import (
	"context"
	"fmt"
	"math"
)

// EarthRadius is the stock released radius shared by domain checks and calls.
const EarthRadius float64 = 6378168

type EarthOperation uint8

const (
	EarthRadiusOperation EarthOperation = iota
	EarthSecToGC
	EarthGCToSec
	EarthLL
	EarthLatitude
	EarthLongitude
)

func (o EarthOperation) Name() string {
	switch o {
	case EarthRadiusOperation:
		return "earth"
	case EarthSecToGC:
		return "sec_to_gc"
	case EarthGCToSec:
		return "gc_to_sec"
	case EarthLL:
		return "ll_to_earth"
	case EarthLatitude:
		return "latitude"
	case EarthLongitude:
		return "longitude"
	}
	return ""
}
func (o EarthOperation) Arity() int {
	if o == EarthRadiusOperation {
		return 0
	}
	if o == EarthLL {
		return 2
	}
	if o <= EarthLongitude {
		return 1
	}
	return -1
}

// EarthCoordinatePlan captures actual descriptor generations, not display names.
type EarthCoordinatePlan struct {
	operation             EarthOperation
	earth, cube, floating *Descriptor
}

func BindEarthCoordinate(o EarthOperation, earth, floating *Descriptor) (EarthCoordinatePlan, error) {
	if o.Name() == "" || earth == nil || !earth.IsDomain() || !earth.DomainBase().Type.IsCube() || floating == nil || floating.ID() != Float8 {
		return EarthCoordinatePlan{}, fmt.Errorf("invalid earth coordinate types")
	}
	return EarthCoordinatePlan{o, earth, earth.DomainBase().Type, floating}, nil
}
func (p EarthCoordinatePlan) Operation() EarthOperation { return p.operation }
func (p EarthCoordinatePlan) Earth() *Descriptor        { return p.earth }
func (p EarthCoordinatePlan) Float() *Descriptor        { return p.floating }
func (p EarthCoordinatePlan) Result() *Descriptor {
	if p.operation == EarthLL {
		return p.earth
	}
	return p.floating
}
func (p EarthCoordinatePlan) Formal() *Descriptor {
	if p.operation >= EarthLatitude {
		return p.earth
	}
	return p.floating
}

func (p EarthCoordinatePlan) Evaluate(ctx context.Context, args []Value, input InputEnvironment) (Value, error) {
	if p.earth == nil || len(args) != p.operation.Arity() {
		return Value{}, fmt.Errorf("invalid earth coordinate plan")
	}
	for _, v := range args {
		if v.typ != p.Formal() {
			return Value{}, fmt.Errorf("foreign earth coordinate argument")
		}
	}
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	for _, v := range args {
		if v.null {
			return Null(p.Result()), nil
		}
	}
	if p.operation == EarthRadiusOperation {
		return Float(p.floating, EarthRadius)
	}
	if p.operation == EarthLL {
		a, err := checkedFloatingBinary(ArithmeticMultiply, Float8, args[0].floating, directRadiansPerDegree)
		if err != nil {
			return Value{}, err
		}
		ca, err := directTrig("cos", a)
		if err != nil {
			return Value{}, err
		}
		b, err := checkedFloatingBinary(ArithmeticMultiply, Float8, args[1].floating, directRadiansPerDegree)
		if err != nil {
			return Value{}, err
		}
		cb, err := directTrig("cos", b)
		if err != nil {
			return Value{}, err
		}
		sb, err := directTrig("sin", b)
		if err != nil {
			return Value{}, err
		}
		rca, err := checkedFloatingBinary(ArithmeticMultiply, Float8, EarthRadius, ca)
		if err != nil {
			return Value{}, err
		}
		x, err := checkedFloatingBinary(ArithmeticMultiply, Float8, rca, cb)
		if err != nil {
			return Value{}, err
		}
		y, err := checkedFloatingBinary(ArithmeticMultiply, Float8, rca, sb)
		if err != nil {
			return Value{}, err
		}
		sa, err := directTrig("sin", a)
		if err != nil {
			return Value{}, err
		}
		z, err := checkedFloatingBinary(ArithmeticMultiply, Float8, EarthRadius, sa)
		if err != nil {
			return Value{}, err
		}
		// The point header and its three owned coordinates share one allocation.
		owned := &struct {
			payload     cubePayload
			coordinates [3]float64
		}{coordinates: [3]float64{x, y, z}}
		owned.payload = cubePayload{dimension: 3, point: true, coordinates: owned.coordinates[:]}
		v := Value{typ: p.cube, cube: &owned.payload}
		if err := p.earth.CheckDomainInput(ctx, v, input); err != nil {
			return Value{}, err
		}
		return p.earth.Wrap(v)
	}
	var n float64
	var err error
	if p.operation >= EarthLatitude {
		v, e := args[0].BaseValue()
		if e != nil {
			return Value{}, e
		}
		if !v.validCube() {
			return Value{}, fmt.Errorf("invalid earth cube payload")
		}
		coordinate := func(i int) float64 {
			if i >= v.cube.dimension {
				return 0
			}
			return v.cube.coordinates[i]
		}
		if p.operation == EarthLongitude {
			n, err = inverseTrig("atan2", coordinate(1), coordinate(0))
		} else {
			n, err = checkedFloatingBinary(ArithmeticDivide, Float8, coordinate(2), EarthRadius)
			if err != nil {
				return Value{}, err
			}
			if n < -1 {
				return Float(p.floating, -90)
			}
			if math.IsNaN(n) || n > 1 {
				return Float(p.floating, 90)
			}
			n, err = inverseTrig("asin", n, 0)
		}
		if err == nil {
			n, err = checkedFloatingBinary(ArithmeticDivide, Float8, n, directRadiansPerDegree)
		}
	} else {
		d := args[0].floating
		if d < 0 {
			return Float(p.floating, 0)
		}
		denominator := 2 * EarthRadius
		if p.operation == EarthGCToSec {
			denominator = EarthRadius
		}
		q, e := checkedFloatingBinary(ArithmeticDivide, Float8, d, denominator)
		if e != nil {
			return Value{}, e
		}
		pi := float64(math.Pi)
		if p.operation == EarthSecToGC {
			if math.IsNaN(q) || q > 1 {
				return Float(p.floating, pi*EarthRadius)
			}
			n, err = inverseTrig("asin", q, 0)
		} else {
			if math.IsNaN(q) || q > pi {
				return Float(p.floating, 2*EarthRadius)
			}
			q, err = checkedFloatingBinary(ArithmeticDivide, Float8, d, 2*EarthRadius)
			if err == nil {
				n, err = directTrig("sin", q)
			}
		}
		if err == nil {
			n, err = checkedFloatingBinary(ArithmeticMultiply, Float8, 2*EarthRadius, n)
		}
	}
	if err != nil {
		return Value{}, err
	}
	return Float(p.floating, n)
}
