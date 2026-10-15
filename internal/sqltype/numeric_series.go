package sqltype

import (
	"context"
	"fmt"
	"math/big"
)

// NumericSeriesCursor owns one invocation's exact decimal state. It must not be
// copied for independent use. Values returned by Next remain immutable after Close.
// Successors are deliberately unpacked: an out-of-format successor can compare
// past stop without ever being published as a numeric Value.
type NumericSeriesCursor struct {
	current, stop, step        big.Int
	publication                expDecimal
	typ                        *Descriptor
	exponent, scale, nextScale int64
	direction                  int
	exhausted                  bool
}

// OpenNumericSeries accepts coerced numeric inputs and zero or one explicit
// step. NULL inputs suppress provider validation, but not caller evaluation.
func OpenNumericSeries(ctx context.Context, start, stop Value, explicitStep ...Value) (NumericSeriesCursor, error) {
	if err := ctx.Err(); err != nil {
		return NumericSeriesCursor{}, err
	}
	if len(explicitStep) > 1 || start.typ == nil || start.typ.id != Numeric || stop.typ != start.typ {
		return NumericSeriesCursor{}, fmt.Errorf("invalid numeric series arguments")
	}
	step := Value{typ: start.typ, text: "1"}
	if len(explicitStep) == 1 {
		step = explicitStep[0]
	}
	if step.typ != start.typ {
		return NumericSeriesCursor{}, fmt.Errorf("invalid numeric series step identity")
	}
	cursor := NumericSeriesCursor{typ: start.typ, scale: start.scale, nextScale: max(start.scale, step.scale)}
	if start.null || stop.null || step.null {
		cursor.exhausted = true
		return cursor, nil
	}
	for _, arg := range []struct {
		value Value
		name  string
	}{{start, "start value"}, {stop, "stop value"}, {step, "step size"}} {
		if arg.value.special == numericNaN {
			return NumericSeriesCursor{}, failure("22023", arg.name+" cannot be NaN")
		}
		if arg.value.special != numericFinite {
			return NumericSeriesCursor{}, failure("22023", arg.name+" cannot be infinity")
		}
	}
	cursor.direction = numericSign(step)
	if cursor.direction == 0 {
		return NumericSeriesCursor{}, failure("22023", "step size cannot equal zero")
	}
	// Ignore zero exponents: zero needs no alignment and must not manufacture
	// a large power merely because its display scale differs.
	cursor.exponent = step.exponent
	for _, v := range []Value{start, stop} {
		if v.text != "" {
			cursor.exponent = min(cursor.exponent, v.exponent)
		}
	}
	var power, ten, exponent big.Int
	ten.SetInt64(10)
	align := func(dst *big.Int, v Value) {
		if v.text == "" {
			return
		}
		dst.SetString(v.text, 10)
		if v.exponent != cursor.exponent {
			exponent.SetInt64(v.exponent - cursor.exponent)
			power.Exp(&ten, &exponent, nil)
			dst.Mul(dst, &power)
		}
		if v.negative {
			dst.Neg(dst)
		}
	}
	align(&cursor.current, start)
	if err := ctx.Err(); err != nil {
		return NumericSeriesCursor{}, err
	}
	align(&cursor.stop, stop)
	if err := ctx.Err(); err != nil {
		return NumericSeriesCursor{}, err
	}
	align(&cursor.step, step)
	if err := ctx.Err(); err != nil {
		return NumericSeriesCursor{}, err
	}
	return cursor, nil
}

func (s *NumericSeriesCursor) Next(ctx context.Context) (Value, bool, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, false, err
	}
	if s.typ == nil {
		return Value{}, false, fmt.Errorf("unopened numeric series cursor")
	}
	if s.exhausted {
		return Null(s.typ), false, nil
	}
	order := s.current.Cmp(&s.stop)
	if s.direction > 0 && order > 0 || s.direction < 0 && order < 0 {
		typ := s.typ
		s.Close()
		s.typ = typ
		return Null(typ), false, nil
	}
	// numericDecimalResult takes absolute value in place; keep its scratch
	// separate from the signed successor state.
	s.publication.coefficient.Set(&s.current)
	s.publication.exponent = s.exponent
	value, err := numericDecimalResult(ctx, s.typ, &s.publication, s.scale)
	if err != nil {
		s.Close()
		return Value{}, false, err
	}
	s.current.Add(&s.current, &s.step)
	s.scale = s.nextScale
	if err := ctx.Err(); err != nil {
		s.Close()
		return Value{}, false, err
	}
	return value, true, nil
}

// Close releases large coefficients and scratch without invalidating emitted
// values. It is idempotent, including after exhaustion or a failed Next.
func (s *NumericSeriesCursor) Close() {
	typ := s.typ
	*s = NumericSeriesCursor{typ: typ, exhausted: true}
}
