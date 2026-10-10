package sqltype

import (
	"context"
	"fmt"
	"math"
)

// DatetimeJustifyPlan retains provider-owned base descriptors, never output
// settings. Stored definitions remint the plan in the destination provider.
type DatetimeJustifyPlan struct {
	name          string
	input, result *Descriptor
}

func BindDatetimeJustify(name string, input, result *Descriptor) (DatetimeJustifyPlan, error) {
	if (name != "justify_days" && name != "justify_hours" && name != "justify_interval") || input == nil || result == nil || input.ID() != Interval || result.ID() != Interval {
		return DatetimeJustifyPlan{}, fmt.Errorf("invalid interval justification plan")
	}
	return DatetimeJustifyPlan{name, input, result}, nil
}

func (p DatetimeJustifyPlan) Name() string { return p.name }
func (p DatetimeJustifyPlan) Retag(retained func(*Descriptor) *Descriptor) (DatetimeJustifyPlan, error) {
	return BindDatetimeJustify(p.name, retained(p.input), retained(p.result))
}

func (p DatetimeJustifyPlan) Evaluate(ctx context.Context, value Value) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if p.result == nil || value.Type() != p.input {
		return Value{}, fmt.Errorf("invalid interval justification input descriptor")
	}
	if value.IsNull() {
		return Null(p.result), nil
	}
	if intervalInfinity(value) != 0 {
		return IntervalValue(p.result, value.months, value.days, value.integer)
	}
	m, d, t := int64(value.months), int64(value.days), value.integer
	// Both checked month carries are deliberate: the release checks its
	// prejustify carry even when a later, opposing clock could cancel it.
	if p.name == "justify_days" || p.name == "justify_interval" && (d > 0 && t > 0 || d < 0 && t < 0) {
		m += d / 30
		d %= 30
		if m < math.MinInt32 || m > math.MaxInt32 {
			return Value{}, failure("22008", "interval out of range")
		}
	}
	if p.name != "justify_days" {
		d += t / microsPerDay
		t %= microsPerDay
		if p.name == "justify_hours" && (d < math.MinInt32 || d > math.MaxInt32) {
			return Value{}, failure("22008", "interval out of range")
		}
	}
	if p.name == "justify_interval" {
		// The day sum is safe: same-sign days were reduced before carrying;
		// other signs cannot overflow the original signed 32-bit day range.
		m += d / 30
		d %= 30
		if m < math.MinInt32 || m > math.MaxInt32 {
			return Value{}, failure("22008", "interval out of range")
		}
	}
	if p.name == "justify_days" {
		if m > 0 && d < 0 {
			m--
			d += 30
		} else if m < 0 && d > 0 {
			m++
			d -= 30
		}
	} else if p.name == "justify_interval" {
		if m > 0 && (d < 0 || d == 0 && t < 0) {
			m--
			d += 30
		} else if m < 0 && (d > 0 || d == 0 && t > 0) {
			m++
			d -= 30
		}
	}
	if p.name != "justify_days" {
		if d > 0 && t < 0 {
			d--
			t += microsPerDay
		} else if d < 0 && t > 0 {
			d++
			t -= microsPerDay
		}
	}
	return IntervalValue(p.result, int32(m), int32(d), t)
}
