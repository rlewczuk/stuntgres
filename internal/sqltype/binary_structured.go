package sqltype

import (
	"encoding/binary"
	"fmt"
	"strings"
	"stuntgres/internal/sqlstate"
)

func (d *Descriptor) binaryNumeric(p []byte, typmod int32) (Value, int, error) {
	if len(p) < 8 {
		return Value{}, 0, binaryShort()
	}
	count := int(binary.BigEndian.Uint16(p))
	weight := int64(int16(binary.BigEndian.Uint16(p[2:])))
	sign := binary.BigEndian.Uint16(p[4:])
	scale := binary.BigEndian.Uint16(p[6:])
	v := Value{typ: d, scale: int64(scale)}
	switch sign {
	case 0:
	case 0x4000:
		v.negative = true
	case 0xc000:
		v.special = numericNaN
	case 0xd000:
		v.special = numericInfinity
	case 0xf000:
		v.special = numericNegativeInfinity
	default:
		return Value{}, 0, failure("22P03", "invalid sign in external \"numeric\" value")
	}
	if scale > 16383 {
		return Value{}, 0, failure("22P03", "invalid scale in external \"numeric\" value")
	}
	n := 8 + count*2
	if len(p) < n {
		return Value{}, 0, binaryShort()
	}
	var digits strings.Builder
	if v.special == numericFinite {
		digits.Grow(count * 4)
	}
	for i := range count {
		value := binary.BigEndian.Uint16(p[8+i*2:])
		if value >= 10000 {
			return Value{}, 0, failure("22P03", "invalid digit in external \"numeric\" value")
		}
		if v.special == numericFinite {
			var group [4]byte
			for j := 3; j >= 0; j-- {
				group[j] = byte(value%10) + '0'
				value /= 10
			}
			digits.Write(group[:])
		}
	}
	if v.special == numericFinite {
		v.text = digits.String()
		v.exponent = (weight - int64(count) + 1) * 4
		v = normalizeNumeric(v)
		v = truncateNumeric(v, int64(scale))
		v = normalizeNumeric(v)
	} else {
		// Wire infinity dscale32 is not the native special-datum scale.
		// Validate every declared digit first, then retain only its identity.
		v = Value{typ: d, special: v.special}
	}
	var err error
	if typmod >= 0 {
		v, err = sizeNumeric(v, typmod)
	}
	if err == nil {
		err = validateNumericFormat(v)
	}
	return v, n, err
}
func appendBinaryNumeric(dst []byte, v Value) []byte {
	sign := uint16(0)
	if v.negative {
		sign = 0x4000
	}
	switch v.special {
	case numericNaN:
		return binary16(binary16(binary16(binary16(dst, 0), 0), 0xc000), 0)
	case numericInfinity:
		return binary16(binary16(binary16(binary16(dst, 0), 0), 0xd000), 32)
	case numericNegativeInfinity:
		return binary16(binary16(binary16(binary16(dst, 0), 0), 0xf000), 32)
	}
	count, weight, low := int64(0), int64(0), int64(0)
	point := int64(len(v.text)) + v.exponent
	if v.text != "" {
		weight = floorDiv(point-1, 4)
		low = floorDiv(v.exponent, 4)
		count = weight - low + 1
	}
	dst = binary16(binary16(binary16(binary16(dst, uint16(count)), uint16(weight)), sign), uint16(v.scale))
	for group := weight; count > 0 && group >= low; group-- {
		digit := uint16(0)
		for place := group*4 + 3; place >= group*4; place-- {
			digit *= 10
			index := point - 1 - place
			if index >= 0 && index < int64(len(v.text)) {
				digit += uint16(v.text[index] - '0')
			}
		}
		dst = binary16(dst, digit)
	}
	return dst
}
func (d *Descriptor) binaryBits(p []byte, typmod int32) (Value, int, error) {
	if len(p) < 4 {
		return Value{}, 0, binaryShort()
	}
	// VARBITMAXLEN = INT_MAX - BITS_PER_BYTE + 1, preserving rounding headroom.
	count := int32(binary.BigEndian.Uint32(p))
	if count < 0 || count > 2147483640 {
		return Value{}, 0, failure("22P03", "invalid length in external bit string")
	}
	n := 4 + (int(count)+7)/8
	if n > len(p) {
		return Value{}, 0, binaryShort()
	}
	if err := checkBitWidth(d.id, int64(count), typmod); err != nil {
		return Value{}, 0, err
	}
	var bits strings.Builder
	bits.Grow(int(count))
	for i := range int(count) {
		bits.WriteByte('0' + (p[4+i/8] >> uint(7-i%8) & 1))
	}
	v, err := BitString(d, bits.String())
	return v, n, err
}
func appendBinaryBits(dst []byte, v Value) []byte {
	dst = binary32(dst, uint32(len(v.text)))
	for i := 0; i < len(v.text); i += 8 {
		b := byte(0)
		for j := 0; j < 8 && i+j < len(v.text); j++ {
			if v.text[i+j] == '1' {
				b |= 1 << uint(7-j)
			}
		}
		dst = append(dst, b)
	}
	return dst
}
func (d *Descriptor) binaryNetwork(p []byte) (Value, int, error) {
	at := 0
	byteValue := func() (byte, error) {
		if at == len(p) {
			return 0, binaryByteShort()
		}
		v := p[at]
		at++
		return v, nil
	}
	family, err := byteValue()
	if err != nil {
		return Value{}, 0, err
	}
	length, bits := 4, 32
	if family == 3 {
		length, bits = 16, 128
	} else if family != 2 {
		return Value{}, 0, failure("22P03", "invalid address family in external \"inet\" value")
	}
	prefix, err := byteValue()
	if err != nil {
		return Value{}, 0, err
	}
	if int(prefix) > bits {
		return Value{}, 0, failure("22P03", "invalid bits in external \"inet\" value")
	}
	if _, err = byteValue(); err != nil {
		return Value{}, 0, err
	}
	size, err := byteValue()
	if err != nil {
		return Value{}, 0, err
	}
	if int(size) != length {
		return Value{}, 0, failure("22P03", "invalid length in external \"inet\" value")
	}
	v := Value{typ: d}
	v.network.family = 4
	if family == 3 {
		v.network.family = 6
	}
	v.network.prefix = prefix
	for i := 0; i < length; i++ {
		v.network.address[i], err = byteValue()
		if err != nil {
			return Value{}, 0, err
		}
	}
	if !v.network.valid(d.id == Cidr) {
		return Value{}, 0, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P03", Message: "invalid external \"cidr\" value", Detail: "Value has bits set to right of mask."}
	}
	return v, at, nil
}
func appendBinaryNetwork(dst []byte, v Value) []byte {
	family, n := byte(2), 4
	if v.network.family == 6 {
		family, n = 3, 16
	}
	cidr := byte(0)
	if v.typ.id == Cidr {
		cidr = 1
	}
	dst = append(dst, family, v.network.prefix, cidr, byte(n))
	return append(dst, v.network.address[:n]...)
}

// Retain only canonical names from genuine builtin descriptors, not codec owners
// from another catalog generation. User wire OIDs never trigger catalog lookup.
var binaryBuiltinNames = func() map[ID]string {
	types := Builtins()
	names := make(map[ID]string, len(types))
	for _, d := range types {
		names[d.ID()] = d.DiagnosticName()
	}
	return names
}()

func binaryOIDMismatch(expected *Descriptor, supplied uint32, record int) error {
	if supplied >= 10000 || uint32(expected.id) >= 10000 || supplied == uint32(expected.id) {
		return nil
	}
	name := binaryBuiltinNames[ID(supplied)]
	if name == "" {
		name = fmt.Sprintf("%d", supplied)
	}
	if record == 0 {
		return failure("42804", fmt.Sprintf("binary data has array element type %d (%s) instead of expected %d (%s)", supplied, name, expected.id, expected.DiagnosticName()))
	}
	return failure("42804", fmt.Sprintf("binary data has type %d (%s) instead of expected %d (%s) in record column %d", supplied, name, expected.id, expected.DiagnosticName(), record))
}
