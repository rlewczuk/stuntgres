package sqltype

import (
	"context"
	"fmt"
)

// MACADDR8Value stores all eight network-order octets inline, including the sign bit.
func MACADDR8Value(d *Descriptor, address uint64) (Value, error) {
	if d == nil || d.id != Macaddr8 {
		return Value{}, fmt.Errorf("invalid macaddr8 value")
	}
	return Value{typ: d, integer: int64(address)}, nil
}

func (v Value) MACADDR8() (uint64, error) {
	if v.null || v.typ == nil || v.typ.id != Macaddr8 {
		return 0, fmt.Errorf("not a macaddr8 value")
	}
	return uint64(v.integer), nil
}

func macaddr8Output(v Value) (string, error) {
	address, err := v.MACADDR8()
	if err != nil {
		return "", err
	}
	const hex = "0123456789abcdef"
	var text [23]byte
	for i := range 8 {
		octet := byte(address >> uint(8*(7-i)))
		text[3*i], text[3*i+1] = hex[octet>>4], hex[octet&15]
		if i < 7 {
			text[3*i+2] = ':'
		}
	}
	return string(text[:]), nil
}

// Follow released macaddr8_in, not MACADDR's scanf grammar. Separators are
// optional at any byte boundary but must agree. Its two-byte loop deliberately
// leaves a final single character unexamined, including after a final separator.
func (d *Descriptor) macaddr8Input(ctx context.Context, text string) (Value, error) {
	at, count := 0, 0
	var address uint64
	var separator byte
	for at < len(text) && space(text[at]) {
		if at&4095 == 0 {
			if err := ctx.Err(); err != nil {
				return Value{}, err
			}
		}
		at++
	}
	for at+1 < len(text) && text[at] != 0 && text[at+1] != 0 {
		if count == 8 {
			goto invalid
		}
		hi, ok := macaddr8Nibble(text[at])
		if !ok {
			goto invalid
		}
		lo, ok := macaddr8Nibble(text[at+1])
		if !ok {
			goto invalid
		}
		address = address<<8 | uint64(hi<<4|lo)
		count++
		at += 2
		if at < len(text) && (text[at] == ':' || text[at] == '-' || text[at] == '.') {
			if separator != 0 && separator != text[at] {
				goto invalid
			}
			separator = text[at]
			at++
		}
		if (count == 6 || count == 8) && at < len(text) && space(text[at]) {
			for at < len(text) && space(text[at]) {
				if at&4095 == 0 {
					if err := ctx.Err(); err != nil {
						return Value{}, err
					}
				}
				at++
			}
			if at != len(text) {
				goto invalid
			}
		}
	}
	if count == 6 {
		address = (address&0xffffff000000)<<16 | 0xfffe000000 | address&0xffffff
	} else if count != 8 {
		goto invalid
	}
	return MACADDR8Value(d, address)
invalid:
	return Value{}, failure("22P02", `invalid input syntax for type macaddr8: "`+text+`"`)
}

func macaddr8Nibble(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}
