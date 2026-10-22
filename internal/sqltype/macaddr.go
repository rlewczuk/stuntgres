package sqltype

import (
	"context"
	"fmt"
)

// MACADDRValue stores six octets in network order in the inline scalar payload.
func MACADDRValue(d *Descriptor, address uint64) (Value, error) {
	if d == nil || d.id != Macaddr || address>>48 != 0 {
		return Value{}, fmt.Errorf("invalid macaddr value")
	}
	return Value{typ: d, integer: int64(address)}, nil
}

func (v Value) MACADDR() (uint64, error) {
	if v.null || v.typ == nil || v.typ.id != Macaddr || v.integer < 0 || uint64(v.integer)>>48 != 0 {
		return 0, fmt.Errorf("not a macaddr value")
	}
	return uint64(v.integer), nil
}

func macaddrOutput(v Value) (string, error) {
	address, err := v.MACADDR()
	if err != nil {
		return "", err
	}
	const hex = "0123456789abcdef"
	var text [17]byte
	for i := range 6 {
		octet := byte(address >> uint(8*(5-i)))
		text[3*i], text[3*i+1] = hex[octet>>4], hex[octet&15]
		if i < 5 {
			text[3*i+2] = ':'
		}
	}
	return string(text[:]), nil
}

// Keep the released macaddr_in candidate order. Zero separators denote adjacent
// conversions; the compact candidates use %2x rather than unbounded %x.
var macaddrFormats = [...]struct {
	width      int
	separators [5]byte
}{
	{0, [5]byte{':', ':', ':', ':', ':'}},
	{0, [5]byte{'-', '-', '-', '-', '-'}},
	{2, [5]byte{0, 0, ':', 0, 0}},
	{2, [5]byte{0, 0, '-', 0, 0}},
	{2, [5]byte{0, '.', 0, '.', 0}},
	{2, [5]byte{0, '-', 0, '-', 0}},
	{2, [5]byte{}},
}

func (d *Descriptor) macaddrInput(ctx context.Context, text string) (Value, error) {
	for _, format := range macaddrFormats {
		var octets [6]uint32
		at, matched := 0, true
		for i := range octets {
			var ok bool
			var err error
			octets[i], at, ok, err = macaddrHex(ctx, text, at, format.width)
			if err != nil {
				return Value{}, err
			}
			if !ok {
				matched = false
				break
			}
			if i < 5 && format.separators[i] != 0 {
				if at == len(text) || text[at] != format.separators[i] {
					matched = false
					break
				}
				at++
			}
		}
		if !matched {
			continue
		}
		for at < len(text) && space(text[at]) {
			if at&4095 == 0 {
				if err := ctx.Err(); err != nil {
					return Value{}, err
				}
			}
			at++
		}
		if at != len(text) {
			continue // scanf's final %1s found trailing non-whitespace.
		}
		var address uint64
		for _, octet := range octets {
			if octet > 255 {
				return Value{}, failure("22003", `invalid octet value in "macaddr" value: "`+text+`"`)
			}
			address = address<<8 | uint64(octet)
		}
		return MACADDRValue(d, address)
	}
	return Value{}, failure("22P02", `invalid input syntax for type macaddr: "`+text+`"`)
}

// Match the pinned Linux64 scanf %x: skip C whitespace, consume signs/prefixes
// within the field width, saturate overflowing unsigned-long magnitudes, then
// negate only nonoverflowing magnitudes and truncate to the destination uint32.
// In particular, scanf consumes an incomplete 0x prefix as a successful zero.
func macaddrHex(ctx context.Context, text string, at, width int) (uint32, int, bool, error) {
	for at < len(text) && space(text[at]) {
		if at&4095 == 0 {
			if err := ctx.Err(); err != nil {
				return 0, at, false, err
			}
		}
		at++
	}
	end := len(text)
	if width != 0 && end-at > width {
		end = at + width
	}
	negative := false
	if at < end && (text[at] == '+' || text[at] == '-') {
		negative = text[at] == '-'
		at++
	}
	start := at
	prefix := at+1 < end && text[at] == '0' && (text[at+1] == 'x' || text[at+1] == 'X')
	if prefix {
		at += 2
	}
	var value uint64
	overflow := false
	for at < end {
		if at&4095 == 0 {
			if err := ctx.Err(); err != nil {
				return 0, at, false, err
			}
		}
		c := text[at]
		var digit uint64
		switch {
		case c >= '0' && c <= '9':
			digit = uint64(c - '0')
		case c >= 'a' && c <= 'f':
			digit = uint64(c - 'a' + 10)
		case c >= 'A' && c <= 'F':
			digit = uint64(c - 'A' + 10)
		default:
			goto done
		}
		if value > (^uint64(0)-digit)/16 {
			overflow = true
		}
		if !overflow {
			value = value*16 + digit
		}
		at++
	}
done:
	if at == start {
		return 0, at, false, nil
	}
	if overflow {
		value = ^uint64(0)
	} else if negative {
		value = -value
	}
	return uint32(value), at, true, nil
}
