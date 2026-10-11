package sqltype

import (
	"context"
	"fmt"
)

// DatetimeBinPlan retains descriptor authority, never a session timezone.
// Both overloads bin elapsed microseconds; civil-to-instant conversion belongs
// to the ordinary child coercion plans.
type DatetimeBinPlan struct {
	stride, input, result *Descriptor
}

func BindDatetimeBin(stride, input, result *Descriptor) (DatetimeBinPlan, error) {
	if stride == nil || stride.ID() != Interval || input == nil || result == nil || input.ID() != result.ID() || (input.ID() != Timestamp && input.ID() != Timestamptz) {
		return DatetimeBinPlan{}, fmt.Errorf("invalid datetime bin plan")
	}
	return DatetimeBinPlan{stride: stride, input: input, result: result}, nil
}

func (p DatetimeBinPlan) Retag(retained func(*Descriptor) *Descriptor) (DatetimeBinPlan, error) {
	return BindDatetimeBin(retained(p.stride), retained(p.input), retained(p.result))
}

func (p DatetimeBinPlan) Evaluate(ctx context.Context, args []Value) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if p.result == nil || len(args) != 3 || args[0].Type() != p.stride || args[1].Type() != p.input || args[2].Type() != p.input {
		return Value{}, fmt.Errorf("invalid datetime bin arguments")
	}
	for _, arg := range args {
		if arg.IsNull() {
			return Null(p.result), nil
		}
	}
	result := args[1]
	result.typ = p.result
	if timestampInfinity(result.integer) != 0 {
		return result, nil
	}
	origin := args[2].integer
	if timestampInfinity(origin) != 0 {
		return Value{}, failure("22008", "origin out of range")
	}
	stride := args[0]
	if intervalInfinity(stride) != 0 {
		return Value{}, failure("22008", "timestamps cannot be binned into infinite intervals")
	}
	if stride.months != 0 {
		return Value{}, failure("0A000", "timestamps cannot be binned into intervals containing months or years")
	}
	// Keep PostgreSQL's component-check order: an overflowing day product is
	// invalid even when a time component could cancel it in wider arithmetic.
	days, ok := intervalMultiply(int64(stride.days), microsPerDay)
	if !ok {
		return Value{}, failure("22008", "interval out of range")
	}
	span, ok := intervalAdd(days, stride.integer)
	if !ok {
		return Value{}, failure("22008", "interval out of range")
	}
	if span <= 0 {
		return Value{}, failure("22008", "stride must be greater than zero")
	}
	delta, ok := arithmeticSubtractInt64(result.integer, origin)
	if !ok {
		return Value{}, failure("22008", "interval out of range")
	}
	remainder := delta % span
	// delta-remainder is representable and lies between zero and delta;
	// adding origin moves from origin toward the source, so cannot overflow.
	result.integer = origin + (delta - remainder)
	if remainder < 0 {
		result.integer, ok = arithmeticSubtractInt64(result.integer, span)
		if !ok || result.integer < timestampMinimum || result.integer >= timestampEnd {
			return Value{}, failure("22008", "timestamp out of range")
		}
	}
	// Match the release branch-specific range check. Exact multiples, including
	// a prerequisite's retained typmod-rounded END, do not take correction.
	return result, nil
}
