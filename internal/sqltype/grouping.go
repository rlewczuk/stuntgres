package sqltype

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Grouping binds NOT DISTINCT equality and a compatible hash to one concrete
// key usage. SQL strict comparison and its NULL semantics are unchanged.
type Grouping struct {
	usage      Usage
	comparison Comparison
}

func BindGrouping(usage Usage) (Grouping, error) {
	if usage.Type == nil || !usage.Type.Concrete() {
		return Grouping{}, fmt.Errorf("invalid grouping usage")
	}
	comparison, err := BindComparison(usage.Type, usage.Type)
	if err != nil {
		return Grouping{}, err
	}
	return Grouping{usage, comparison}, nil
}
func (g Grouping) check(v Value) error {
	if g.usage.Type == nil || v.typ != g.usage.Type {
		return fmt.Errorf("invalid grouping value")
	}
	if !v.null && v.typ.id == Regclass {
		if len(v.text) == 0 || len(v.text) > 1 && v.text[0] == '0' {
			return fmt.Errorf("invalid canonical regclass grouping value")
		}
		for i := range len(v.text) {
			if v.text[i] < '0' || v.text[i] > '9' {
				return fmt.Errorf("invalid canonical regclass grouping value")
			}
		}
		if _, err := strconv.ParseUint(v.text, 10, 32); err != nil {
			return fmt.Errorf("invalid canonical regclass grouping value")
		}
	}
	return nil
}
func (g Grouping) Equal(ctx context.Context, a, b Value) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := g.check(a); err != nil {
		return false, err
	}
	if err := g.check(b); err != nil {
		return false, err
	}
	if a.null || b.null {
		return a.null == b.null, ctx.Err()
	}
	if a.typ.id == Regclass {
		return a.text == b.text, ctx.Err()
	}
	n, err := g.comparison.Compare(ctx, a, b)
	if err != nil {
		return false, err
	}
	return n == 0, ctx.Err()
}
func (g Grouping) Hash(ctx context.Context, v Value) (uint64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if err := g.check(v); err != nil {
		return 0, err
	}
	h := uint64(14695981039346656037)
	byteHash := func(b byte) { h = (h ^ uint64(b)) * 1099511628211 }
	integerHash := func(n uint64) {
		for range 8 {
			byteHash(byte(n))
			n >>= 8
		}
	}
	textHash := func(s string) error {
		for i := range len(s) {
			if i&1023 == 0 {
				if err := ctx.Err(); err != nil {
					return err
				}
			}
			byteHash(s[i])
		}
		return nil
	}
	integerHash(uint64(v.typ.id))
	if v.null {
		byteHash(0)
		return h, ctx.Err()
	}
	byteHash(1)
	switch v.typ.id {
	case Bool:
		if v.boolean {
			byteHash(1)
		} else {
			byteHash(0)
		}
	case Int2, Int4, Int8, Date, Time, Timestamp:
		integerHash(uint64(v.integer))
	case Float4, Float8:
		bits := math.Float64bits(v.floating)
		if math.IsNaN(v.floating) {
			bits = 0x7ff8000000000000
		} else if v.floating == 0 {
			bits = 0
		}
		integerHash(bits)
	case Numeric:
		byteHash(v.special)
		if v.special == numericFinite {
			v = normalizeNumeric(v)
			if v.negative {
				byteHash(1)
			} else {
				byteHash(0)
			}
			integerHash(uint64(v.exponent))
			if err := textHash(v.text); err != nil {
				return 0, err
			}
		}
	case Text, VarChar, BPChar, Name, Bytea, Regclass:
		s := v.text
		if v.typ.id == BPChar {
			s = strings.TrimRight(s, " ")
		}
		if err := textHash(s); err != nil {
			return 0, err
		}
	case UUID:
		for _, b := range v.uuid {
			byteHash(b)
		}
	default:
		return 0, fmt.Errorf("unsupported grouping type")
	}
	return h, ctx.Err()
}
