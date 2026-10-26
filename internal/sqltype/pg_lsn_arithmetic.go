package sqltype

import (
	"context"
	"errors"
	"strconv"

	"stuntgres/internal/sqlstate"
)

func pgLSNNumeric(d *Descriptor, n uint64, negative bool) Value {
	if n == 0 {
		return Value{typ: d}
	}
	return normalizeNumeric(Value{typ: d, text: strconv.FormatUint(n, 10), negative: negative})
}

func (p Arithmetic) pgLSNBinary(ctx context.Context, a, b Value) (Value, error) {
	if p.left.id == PgLSN && p.right.id == PgLSN {
		x, y := uint64(a.integer), uint64(b.integer)
		if x < y {
			return pgLSNNumeric(p.result, y-x, true), nil
		}
		return pgLSNNumeric(p.result, x-y, false), nil
	}
	// The released numeric-left operator is an SQL wrapper. Preserve its
	// provenance by signature, independently of SQL spelling or parameter route.
	reverse := p.left.id == Numeric
	if reverse {
		a, b = b, a
	}
	value, err := p.pgLSNOffset(ctx, a, b)
	if reverse && err != nil {
		var diagnostic *sqlstate.Error
		if errors.As(err, &diagnostic) {
			copy := *diagnostic
			if copy.Context != "" {
				copy.Context += "\n"
			}
			copy.Context += "SQL function \"numeric_pl_pg_lsn\" statement 1"
			return Value{}, sqlstate.WithDiagnostic(err, &copy)
		}
	}
	return value, err
}

func (p Arithmetic) pgLSNOffset(ctx context.Context, lsn, offset Value) (Value, error) {
	if offset.special == numericNaN {
		action := "add NaN to"
		if p.operator == ArithmeticSubtract {
			action = "subtract NaN from"
		}
		return Value{}, failure("0A000", "cannot "+action+" pg_lsn")
	}
	if offset.special != numericFinite {
		return Value{}, failure("0A000", "cannot convert infinity to pg_lsn")
	}
	number := pgLSNNumeric(offset.typ, uint64(lsn.integer), false)
	completed, err := numericArithmetic(ctx, p.operator, number, offset)
	if err != nil {
		return Value{}, err
	}
	completed = roundNumeric(completed, 0)
	if completed.text == "" {
		return PGLSNValue(p.result, 0)
	}
	if completed.negative || completed.exponent < 0 || int64(len(completed.text))+completed.exponent > 20 {
		return Value{}, failure("22023", "pg_lsn out of range")
	}
	var n uint64
	const limit = ^uint64(0)
	for i := range int64(len(completed.text)) + completed.exponent {
		digit := uint64(0)
		if i < int64(len(completed.text)) {
			digit = uint64(completed.text[i] - '0')
		}
		if n > (limit-digit)/10 {
			return Value{}, failure("22023", "pg_lsn out of range")
		}
		n = n*10 + digit
	}
	return PGLSNValue(p.result, n)
}
