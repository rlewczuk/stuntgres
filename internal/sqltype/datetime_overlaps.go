package sqltype

import (
	"context"
	"fmt"
)

// DatetimeOverlapsPlan delegates all arithmetic and ordering to the selected
// homogeneous temporal providers. Local clock-duration conversion is confined
// to this family; it does not grant a general TIME-to-INTERVAL cast.
type DatetimeOverlapsPlan struct {
	endpoint, interval, result, clock *Descriptor
	duration                          [2]bool
	localClock                        [4]bool
	addition                          Arithmetic
	comparison                        Comparison
}

func BindDatetimeOverlaps(endpoint, interval, result, clock *Descriptor, duration [2]bool, localClock [4]bool) (DatetimeOverlapsPlan, error) {
	if endpoint == nil || interval == nil || result == nil || clock == nil || interval.id != Interval || result.id != Bool || clock.id != Time {
		return DatetimeOverlapsPlan{}, fmt.Errorf("invalid OVERLAPS descriptors")
	}
	switch endpoint.id {
	case Time, Timestamp, Timestamptz:
	case Timetz:
		if duration[0] || duration[1] {
			return DatetimeOverlapsPlan{}, fmt.Errorf("invalid TIMETZ duration")
		}
	default:
		return DatetimeOverlapsPlan{}, fmt.Errorf("invalid OVERLAPS endpoint")
	}
	for i, local := range localClock {
		if local && (i%2 == 0 || !duration[i/2]) {
			return DatetimeOverlapsPlan{}, fmt.Errorf("invalid local clock duration")
		}
	}
	comparison, err := BindComparison(endpoint, endpoint)
	if err != nil {
		return DatetimeOverlapsPlan{}, err
	}
	p := DatetimeOverlapsPlan{endpoint: endpoint, interval: interval, result: result, clock: clock, duration: duration, localClock: localClock, comparison: comparison}
	if duration[0] || duration[1] {
		p.addition, err = BindArithmetic(ArithmeticAdd, endpoint, interval, endpoint)
		if err != nil {
			return DatetimeOverlapsPlan{}, err
		}
	}
	return p, nil
}

func (p DatetimeOverlapsPlan) ArgumentType(i int) *Descriptor {
	if p.localClock[i] {
		return p.clock
	}
	return p.SignatureType(i)
}
func (p DatetimeOverlapsPlan) SignatureType(i int) *Descriptor {
	if i%2 != 0 && p.duration[i/2] {
		return p.interval
	}
	return p.endpoint
}
func (p DatetimeOverlapsPlan) Duration() [2]bool      { return p.duration }
func (p DatetimeOverlapsPlan) LocalClock() [4]bool    { return p.localClock }
func (p DatetimeOverlapsPlan) SessionDependent() bool { return p.addition.SessionDependent() }
func (p DatetimeOverlapsPlan) Retag(retained func(*Descriptor) *Descriptor) (DatetimeOverlapsPlan, error) {
	return BindDatetimeOverlaps(retained(p.endpoint), retained(p.interval), retained(p.result), retained(p.clock), p.duration, p.localClock)
}

// DurationEnd uses the selected arithmetic and local clock conversion without
// admitting a global cast or adding a second temporal arithmetic implementation.
func (p DatetimeOverlapsPlan) DurationEnd(ctx context.Context, pair int, start, duration Value, input InputEnvironment) (Value, error) {
	i := pair*2 + 1
	if start.typ != p.endpoint || duration.typ != p.ArgumentType(i) || !p.duration[pair] {
		return Value{}, fmt.Errorf("invalid OVERLAPS duration authority")
	}
	if p.localClock[i] {
		if duration.null {
			duration = Null(p.interval)
		} else {
			var err error
			duration, err = IntervalValue(p.interval, 0, 0, duration.integer)
			if err != nil {
				return Value{}, err
			}
		}
	}
	return p.addition.Binary(ctx, start, duration, input)
}

// The error slot identifies a failed duration addition; -1 means an error
// outside a wrapper body. The engine owns SQL wrapper/inlining diagnostics.
func (p DatetimeOverlapsPlan) Evaluate(ctx context.Context, values [4]Value, folded [2]Value, input InputEnvironment) (Value, int, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, -1, err
	}
	for i, v := range values {
		if v.typ != p.ArgumentType(i) {
			return Value{}, -1, fmt.Errorf("invalid OVERLAPS argument authority")
		}
	}
	for pair, duration := range p.duration {
		if !duration {
			continue
		}
		i := pair*2 + 1
		if folded[pair].typ != nil {
			if folded[pair].typ != p.endpoint {
				return Value{}, -1, fmt.Errorf("invalid folded OVERLAPS authority")
			}
			values[i] = folded[pair]
			continue
		}
		end, err := p.DurationEnd(ctx, pair, values[i-1], values[i], input)
		if err != nil {
			return Value{}, pair, err
		}
		values[i] = end
	}
	for pair := range 2 {
		i := pair * 2
		if values[i].null {
			values[i], values[i+1] = values[i+1], values[i]
		} else if !values[i+1].null {
			order, err := p.comparison.Compare(ctx, values[i], values[i+1], input)
			if err != nil {
				return Value{}, -1, err
			}
			if order > 0 {
				values[i], values[i+1] = values[i+1], values[i]
			}
		}
	}
	if values[0].null || values[2].null {
		return Null(p.result), -1, nil
	}
	leftComplete, rightComplete := !values[1].null, !values[3].null
	if !leftComplete && !rightComplete {
		return Null(p.result), -1, nil
	}
	if !leftComplete || !rightComplete {
		lo, hi, k := values[0], values[1], values[2]
		if !leftComplete {
			lo, hi, k = values[2], values[3], values[0]
		}
		lower, err := p.comparison.Compare(ctx, lo, k, input)
		if err != nil {
			return Value{}, -1, err
		}
		upper, err := p.comparison.Compare(ctx, k, hi, input)
		if err != nil {
			return Value{}, -1, err
		}
		if lower < 0 && upper < 0 {
			return Boolean(p.result, true), -1, nil
		}
		return Null(p.result), -1, nil
	}
	starts, err := p.comparison.Compare(ctx, values[0], values[2], input)
	if err != nil {
		return Value{}, -1, err
	}
	if starts == 0 {
		return Boolean(p.result, true), -1, nil
	}
	if starts < 0 {
		order, err := p.comparison.Compare(ctx, values[2], values[1], input)
		if err != nil {
			return Value{}, -1, err
		}
		return Boolean(p.result, order < 0), -1, nil
	}
	order, err := p.comparison.Compare(ctx, values[0], values[3], input)
	if err != nil {
		return Value{}, -1, err
	}
	return Boolean(p.result, order < 0), -1, nil
}
