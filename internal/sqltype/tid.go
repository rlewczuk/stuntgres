package sqltype

import (
	"context"
	"fmt"
	"strconv"
)

// TIDValue owns an externally supplied tuple identifier, not a physical row.
func TIDValue(d *Descriptor, block uint32, offset uint16) (Value, error) {
	if d == nil || d.id != Tid {
		return Value{}, fmt.Errorf("invalid tid descriptor")
	}
	return Value{typ: d, integer: int64(uint64(block)<<16 | uint64(offset))}, nil
}

func (v Value) TID() (uint32, uint16, error) {
	if v.null || v.typ == nil || v.typ.id != Tid || v.integer < 0 || uint64(v.integer)>>48 != 0 {
		return 0, 0, fmt.Errorf("not a tid value")
	}
	return uint32(uint64(v.integer) >> 16), uint16(v.integer), nil
}

func tidOutput(v Value) (string, error) {
	block, offset, err := v.TID()
	if err != nil {
		return "", err
	}
	return "(" + strconv.FormatUint(uint64(block), 10) + "," + strconv.FormatUint(uint64(offset), 10) + ")", nil
}

func (d *Descriptor) tidInput(ctx context.Context, text string) (Value, error) {
	invalid := func() (Value, error) {
		return Value{}, failure("22P02", `invalid input syntax for type tid: "`+text+`"`)
	}
	var starts [2]int
	n := 0
	for i := 0; i < len(text) && text[i] != 0 && text[i] != ')' && n < 2; i++ {
		if i&4095 == 0 {
			if err := ctx.Err(); err != nil {
				return Value{}, err
			}
		}
		if text[i] == ',' || text[i] == '(' && n == 0 {
			starts[n] = i + 1
			n++
		}
	}
	if n != 2 {
		return invalid()
	}
	block, end, ok, err := tidUnsigned(ctx, text, starts[0])
	if err != nil {
		return Value{}, err
	}
	if !ok || end >= len(text) || text[end] != ',' || block != uint64(uint32(block)) && block != uint64(int64(int32(uint32(block)))) {
		return invalid()
	}
	offset, end, ok, err := tidUnsigned(ctx, text, starts[1])
	if err != nil {
		return Value{}, err
	}
	if !ok || end >= len(text) || text[end] != ')' || offset > 65535 {
		return invalid()
	}
	return TIDValue(d, uint32(block), uint16(offset))
}

// Match Linux64 strtoul(base10), including its original-pointer no-digit result
// and unsigned negation. The released PG18 scanner deliberately accepts those
// empty components whose original pointer already names the required delimiter.
func tidUnsigned(ctx context.Context, text string, start int) (uint64, int, bool, error) {
	i := start
	for i < len(text) {
		if i&4095 == 0 {
			if err := ctx.Err(); err != nil {
				return 0, i, false, err
			}
		}
		switch text[i] {
		case ' ', '\t', '\n', '\v', '\f', '\r':
			i++
		default:
			goto number
		}
	}
number:
	negative := false
	if i < len(text) && (text[i] == '+' || text[i] == '-') {
		negative = text[i] == '-'
		i++
	}
	digits := i
	var value uint64
	for i < len(text) && text[i] >= '0' && text[i] <= '9' {
		if i&4095 == 0 {
			if err := ctx.Err(); err != nil {
				return 0, i, false, err
			}
		}
		digit := uint64(text[i] - '0')
		if value > (^uint64(0)-digit)/10 {
			return 0, i, false, nil
		}
		value = value*10 + digit
		i++
	}
	if i == digits {
		return 0, start, true, nil
	}
	if negative {
		value = -value
	}
	return value, i, true, nil
}
