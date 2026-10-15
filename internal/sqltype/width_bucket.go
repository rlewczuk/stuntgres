package sqltype

import (
	"context"
	"fmt"
	"math"
)

func (p MathPlan) Equal(other MathPlan) bool {
	comparison, next := p.comparison, other.comparison
	p.comparison, other.comparison = Comparison{}, Comparison{}
	return p == other && comparison.Equal(next)
}
func (p MathPlan) InputCollation() (CollationRef, bool) { return p.comparison.InputCollation() }
func (p MathPlan) SessionDependent() bool               { return p.comparison.SessionDependent() }

func BindWidthBucketThreshold(result, scalar, array Usage, ref CollationRef, conflict bool) (MathPlan, error) {
	if !result.Valid() || result != result.Type.Usage() || result.Type.id != Int4 || !scalar.Valid() || !array.Valid() || !array.Type.IsArray() || array.Type.Element() != scalar.Type {
		return MathPlan{}, fmt.Errorf("invalid threshold bucket signature")
	}
	comparison, err := bindArrayComparisonElement(array.Type, ref, conflict)
	if err != nil {
		return MathPlan{}, err
	}
	comparison.inputCollation, comparison.collationIndeterminate = ref, conflict
	return MathPlan{name: "width_bucket", args: [4]Usage{scalar, array}, arity: 2, result: result, comparison: *comparison}, nil
}

func (p MathPlan) thresholdBucket(ctx context.Context, operand, thresholds Value, input InputEnvironment) (Value, error) {
	if thresholds.array == nil {
		return Value{}, fmt.Errorf("missing threshold array payload")
	}
	if thresholds.ArrayRank() > 1 {
		return Value{}, failure("2202E", "thresholds must be one-dimensional array")
	}
	for i := range thresholds.ArrayLen() {
		if err := ctx.Err(); err != nil {
			return Value{}, err
		}
		if thresholds.ArrayElement(i).null {
			return Value{}, failure("22004", "thresholds array must not contain NULLs")
		}
	}
	if err := p.comparison.demandOrdering(); err != nil {
		return Value{}, err
	}
	low, high := 0, thresholds.ArrayLen()
	for low < high {
		if err := ctx.Err(); err != nil {
			return Value{}, err
		}
		mid := low + (high-low)/2
		sign, err := p.comparison.Compare(ctx, operand, thresholds.ArrayElement(mid), input)
		if err != nil {
			return Value{}, err
		}
		if sign < 0 {
			high = mid
		} else {
			low = mid + 1
		}
	}
	return Integer(p.result.Type, int64(low))
}

func widthBucket(ctx context.Context, result *Descriptor, operand, lower, upper Value, count int64) (Value, error) {
	if count <= 0 {
		return Value{}, failure("2201G", "count must be greater than zero")
	}
	var direction, fromLower, fromUpper int
	if operand.typ.id == Numeric {
		if operand.special == numericNaN || lower.special == numericNaN || upper.special == numericNaN {
			return Value{}, failure("2201G", "operand, lower bound, and upper bound cannot be NaN")
		}
		if lower.special != numericFinite || upper.special != numericFinite {
			return Value{}, failure("2201G", "lower and upper bounds must be finite")
		}
		direction = compareNumeric(lower, upper)
		fromLower, fromUpper = compareNumeric(operand, lower), compareNumeric(operand, upper)
	} else {
		x, low, high := operand.floating, lower.floating, upper.floating
		if math.IsNaN(x) || math.IsNaN(low) || math.IsNaN(high) {
			return Value{}, failure("2201G", "operand, lower bound, and upper bound cannot be NaN")
		}
		if math.IsInf(low, 0) || math.IsInf(high, 0) {
			return Value{}, failure("2201G", "lower and upper bounds must be finite")
		}
		if low < high {
			direction = -1
		} else if low > high {
			direction = 1
		}
		if x < low {
			fromLower = -1
		} else if x > low {
			fromLower = 1
		}
		if x < high {
			fromUpper = -1
		} else if x > high {
			fromUpper = 1
		}
	}
	if direction == 0 {
		return Value{}, failure("2201G", "lower bound cannot equal upper bound")
	}
	if direction < 0 && fromLower < 0 || direction > 0 && fromLower > 0 {
		return Integer(result, 0)
	}
	if direction < 0 && fromUpper >= 0 || direction > 0 && fromUpper <= 0 {
		if count == math.MaxInt32 {
			return Value{}, failure("22003", "integer out of range")
		}
		return Integer(result, count+1)
	}
	var bucket int64
	if operand.typ.id == Numeric {
		var distance, span, negativeLower expDecimal
		distance.setValue(operand)
		span.setValue(upper)
		negativeLower.setValue(lower)
		negativeLower.coefficient.Neg(&negativeLower.coefficient)
		var w expScratch
		w.add(&distance, &negativeLower)
		w.add(&span, &negativeLower)
		distance.coefficient.Abs(&distance.coefficient)
		span.coefficient.Abs(&span.coefficient)
		w.twice.SetInt64(count)
		distance.coefficient.Mul(&distance.coefficient, &w.twice)
		shift := distance.exponent - span.exponent
		if shift > 0 {
			distance.coefficient.Mul(&distance.coefficient, w.tenPower(shift))
		} else if shift < 0 {
			span.coefficient.Mul(&span.coefficient, w.tenPower(-shift))
		}
		if err := ctx.Err(); err != nil {
			return Value{}, err
		}
		w.product.Quo(&distance.coefficient, &span.coefficient)
		bucket = w.product.Int64()
	} else {
		x, low, high := operand.floating, lower.floating, upper.floating
		if direction > 0 {
			x, low, high = -x, -low, -high
		}
		var ratio float64
		if math.IsInf(high-low, 0) {
			ratio = (x/2 - low/2) / (high/2 - low/2)
		} else {
			ratio = (x - low) / (high - low)
		}
		bucket = int64(float64(count) * ratio)
		if bucket >= count {
			bucket = count - 1
		}
	}
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	return Integer(result, bucket+1)
}
