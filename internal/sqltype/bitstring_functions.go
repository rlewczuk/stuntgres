package sqltype

import (
	"context"
	"math"
	"strings"
)

// evaluateBits consumes canonical immutable digits after the owning plan has
// validated descriptor identity, cancellation and strict NULL arguments.
func (p BinaryPlan) evaluateBits(ctx context.Context, values []Value) (Value, error) {
	source := values[0].text
	switch p.name {
	case "length", "bit_length", "octet_length":
		count := int64(len(source))
		if p.name == "octet_length" {
			count /= 8
			if len(source)%8 != 0 {
				count++
			}
		}
		return Integer(p.result.Type, count)
	case "bit_count":
		var count int64
		for i := range len(source) {
			if i&4095 == 0 {
				if err := ctx.Err(); err != nil {
					return Value{}, err
				}
			}
			if source[i] == '1' {
				count++
			}
		}
		if err := ctx.Err(); err != nil {
			return Value{}, err
		}
		return Integer(p.result.Type, count)
	case "substring":
		lo, hi, err := substringRange(len(source), values[1:])
		if err != nil {
			return Value{}, err
		}
		return Value{typ: p.result.Type, text: source[lo:hi]}, nil
	case "position":
		if len(source) == 0 {
			return Integer(p.result.Type, 0)
		}
		index, err := binaryPosition(ctx, source, values[1].text)
		if err != nil {
			return Value{}, err
		}
		return Integer(p.result.Type, int64(index)+1)
	case "overlay":
		start := values[2].integer
		if start <= 0 {
			return Value{}, failure("22011", "negative substring length not allowed")
		}
		replacement := values[1].text
		count := int64(len(replacement))
		if p.arity == 4 {
			count = values[3].integer
		}
		// Both explicit indexes are INT4; check an omitted payload length before
		// addition as well, so even a theoretical huge replacement cannot wrap.
		if count > math.MaxInt32-start || count < math.MinInt32-start {
			return Value{}, failure("22003", "integer out of range")
		}
		end := start + count
		lo := int(min(start-1, int64(len(source))))
		hi := int(min(max(end, 1)-1, int64(len(source))))
		suffix := len(source) - hi
		maxSize := int(^uint(0) >> 1)
		if lo > maxSize-len(replacement) || lo+len(replacement) > maxSize-suffix {
			return Value{}, failure("22003", "integer out of range")
		}
		if len(replacement) == 0 && lo == hi {
			return Value{typ: p.result.Type, text: source}, nil
		}
		var out strings.Builder
		out.Grow(lo + len(replacement) + suffix)
		for _, part := range [3]string{source[:lo], replacement, source[hi:]} {
			if err := stringWrite(ctx, &out, part); err != nil {
				return Value{}, err
			}
		}
		if err := ctx.Err(); err != nil {
			return Value{}, err
		}
		return Value{typ: p.result.Type, text: out.String()}, nil
	}
	panic("invalid selected bit plan")
}

// PostgreSQL's VARBITMAXLEN is INT_MAX - BITS_PER_BYTE + 1, independent
// of the much smaller maximum declared typmod.
func bitStringConcatSize(left, right int) (int, error) {
	if left > math.MaxInt32-7-right {
		return 0, failure("54000", "bit string length exceeds the maximum allowed (2147483640)")
	}
	return left + right, nil
}

func (p BinaryPlan) concatenateBits(ctx context.Context, a, b Value) (Value, error) {
	size, err := bitStringConcatSize(len(a.text), len(b.text))
	if err != nil {
		return Value{}, err
	}
	if len(a.text) == 0 {
		return b, nil
	}
	if len(b.text) == 0 {
		return a, nil
	}
	var out strings.Builder
	out.Grow(size)
	if err := stringWrite(ctx, &out, a.text); err != nil {
		return Value{}, err
	}
	if err := stringWrite(ctx, &out, b.text); err != nil {
		return Value{}, err
	}
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	return Value{typ: p.result.Type, text: out.String()}, nil
}
