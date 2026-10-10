package sqltype

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

const maximumBitLength int32 = 83886080

func isBitString(id ID) bool { return id == Bit || id == VarBit }

// BitString constructs a checked immutable canonical binary datum.
func BitString(d *Descriptor, bits string) (Value, error) {
	if d == nil || !isBitString(d.id) || d.IsDomain() {
		return Value{}, fmt.Errorf("not a bit string descriptor")
	}
	for i := range len(bits) {
		if bits[i] != '0' && bits[i] != '1' {
			return Value{}, fmt.Errorf("noncanonical bit string")
		}
	}
	return Value{typ: d, text: bits}, nil
}

func (v Value) Bits() (string, error) {
	if v.typ == nil || v.null || !isBitString(v.typ.id) {
		return "", fmt.Errorf("not a bit string value")
	}
	return v.text, nil
}

func checkBitWidth(id ID, length int64, typmod int32) error {
	if typmod < 0 {
		return nil
	}
	if id == Bit && length != int64(typmod) {
		return failure("22026", fmt.Sprintf("bit string length %d does not match type bit(%d)", length, typmod))
	}
	if id == VarBit && length > int64(typmod) {
		return failure("22001", fmt.Sprintf("bit string too long for type bit varying(%d)", typmod))
	}
	return nil
}

func sizeBitString(v Value, typmod int32) (Value, error) {
	if !v.null {
		if err := checkBitWidth(v.typ.id, int64(len(v.text)), typmod); err != nil {
			return Value{}, err
		}
	}
	return v, nil
}

func (d *Descriptor) bitInput(text string, typmod int32) (Value, error) {
	hex := false
	if len(text) > 0 {
		switch text[0] {
		case 'b', 'B':
			text = text[1:]
		case 'x', 'X':
			hex = true
			text = text[1:]
		}
	}
	length := int64(len(text))
	if hex {
		if len(text) > int(^uint(0)>>1)/4 {
			return Value{}, failure("54000", "bit string is too long")
		}
		length *= 4
	}
	// Bounded provider input checks encoded width before validating digits.
	// SQL constants and parameters deliberately input at typmod -1 instead.
	if err := checkBitWidth(d.id, length, typmod); err != nil {
		return Value{}, err
	}
	for i := range len(text) {
		c := text[i]
		if !hex && (c == '0' || c == '1') || hex && (c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			continue
		}
		_, n := utf8.DecodeRuneInString(text[i:])
		family := "binary"
		if hex {
			family = "hexadecimal"
		}
		return Value{}, failure("22P02", fmt.Sprintf("\"%s\" is not a valid %s digit", text[i:i+n], family))
	}
	if !hex {
		return Value{typ: d, text: text}, nil
	}
	var output strings.Builder
	output.Grow(int(length))
	for i := range len(text) {
		c := text[i]
		var digit byte
		switch {
		case c <= '9':
			digit = c - '0'
		case c <= 'F':
			digit = c - 'A' + 10
		default:
			digit = c - 'a' + 10
		}
		for shift := 3; shift >= 0; shift-- {
			output.WriteByte('0' + ((digit >> shift) & 1))
		}
	}
	return Value{typ: d, text: output.String()}, nil
}

func (d *Descriptor) bitModifiers(modifiers []int32) (Usage, error) {
	u := d.Usage()
	if len(modifiers) != 1 {
		return u, failure("22023", "invalid type modifier")
	}
	n := modifiers[0]
	if n < 1 {
		return u, failure("22023", "length for type "+d.name+" must be at least 1")
	}
	if n > maximumBitLength {
		return u, failure("22023", "length for type "+d.name+" cannot exceed 83886080")
	}
	u.Typmod = n
	return u, nil
}
