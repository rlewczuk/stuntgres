package sqltype

import (
	"context"
	"fmt"
	"strings"
	"stuntgres/internal/sqlstate"
)

// REL_18_6 pg_cast.dat: direct edges only; automatic string I/O is separate.
var releasedCasts = [...]castSpec{
	{XML, Text, Assignment, viaIO, castCatalogFact{}},
	{XML, VarChar, Assignment, viaIO, castCatalogFact{}},
	{XML, BPChar, Assignment, viaIO, castCatalogFact{}},
	{Text, XML, Explicit, viaIO, castCatalogFact{}},
	{VarChar, XML, Explicit, viaIO, castCatalogFact{}},
	{BPChar, XML, Explicit, viaIO, castCatalogFact{}},
	{Text, BPChar, Implicit, stringConversion, castCatalogFact{}},
	{Text, VarChar, Implicit, stringConversion, castCatalogFact{}},
	{BPChar, Text, Implicit, stringConversion, castCatalogFact{}},
	{BPChar, VarChar, Implicit, stringConversion, castCatalogFact{}},
	{VarChar, Text, Implicit, stringConversion, castCatalogFact{}},
	{VarChar, BPChar, Implicit, stringConversion, castCatalogFact{}},
	{Int4, Bool, Explicit, integerBoolean, castCatalogFact{}},
	{Bool, Int4, Explicit, booleanInteger, castCatalogFact{}},
	{Bool, Text, Assignment, booleanText, castCatalogFact{}},
	{Bool, VarChar, Assignment, booleanText, castCatalogFact{}},
	{Bool, BPChar, Assignment, booleanText, castCatalogFact{}},
	{Inet, Text, Assignment, networkTextConversion, castCatalogFact{}},
	{Inet, VarChar, Assignment, networkTextConversion, castCatalogFact{}},
	{Inet, BPChar, Assignment, networkTextConversion, castCatalogFact{}},
	{Cidr, Text, Assignment, networkTextConversion, castCatalogFact{}},
	{Cidr, VarChar, Assignment, networkTextConversion, castCatalogFact{}},
	{Cidr, BPChar, Assignment, networkTextConversion, castCatalogFact{}},
	{Cidr, Inet, Implicit, networkConversion, castCatalogFact{}},
	{Inet, Cidr, Assignment, networkConversion, castCatalogFact{}},
	{Regproc, Regprocedure, Implicit, oidConversion, castCatalogFact{}},
	{Regprocedure, Regproc, Implicit, oidConversion, castCatalogFact{}},
	{Bit, VarBit, Implicit, bitStringConversion, castCatalogFact{}},
	{VarBit, Bit, Implicit, bitStringConversion, castCatalogFact{}},
	{JSON, JSONB, Assignment, viaIO, castCatalogFact{}},
	{JSONB, JSON, Assignment, viaIO, castCatalogFact{}},
	{Char, BPChar, Assignment, internalCharConversion, castCatalogFact{}},
	{Char, VarChar, Assignment, internalCharConversion, castCatalogFact{}},
	{Text, Char, Assignment, internalCharConversion, castCatalogFact{}},
	{VarChar, Char, Assignment, internalCharConversion, castCatalogFact{}},
	{BPChar, Char, Assignment, internalCharConversion, castCatalogFact{}},
	{Char, Int4, Explicit, internalCharConversion, castCatalogFact{}},
	{Int4, Char, Explicit, internalCharConversion, castCatalogFact{}},
	{Int2, Bytea, Explicit, integerByteaConversion, castCatalogFact{}},
	{Int4, Bytea, Explicit, integerByteaConversion, castCatalogFact{}},
	{Int8, Bytea, Explicit, integerByteaConversion, castCatalogFact{}},
	{Bytea, Int2, Explicit, integerByteaConversion, castCatalogFact{}},
	{Bytea, Int4, Explicit, integerByteaConversion, castCatalogFact{}},
	{Bytea, Int8, Explicit, integerByteaConversion, castCatalogFact{}},
	{Int4, Bit, Explicit, integerBitConversion, castCatalogFact{}},
	{Int8, Bit, Explicit, integerBitConversion, castCatalogFact{}},
	{Bit, Int4, Explicit, integerBitConversion, castCatalogFact{}},
	{Bit, Int8, Explicit, integerBitConversion, castCatalogFact{}},
	{Macaddr, Macaddr8, Implicit, macConversion, castCatalogFact{}},
	{Macaddr8, Macaddr, Implicit, macConversion, castCatalogFact{}},
	{JSONB, Bool, Explicit, jsonbScalarConversion, castCatalogFact{}},
	{JSONB, Numeric, Explicit, jsonbScalarConversion, castCatalogFact{}},
	{JSONB, Int2, Explicit, jsonbScalarConversion, castCatalogFact{}},
	{JSONB, Int4, Explicit, jsonbScalarConversion, castCatalogFact{}},
	{JSONB, Int8, Explicit, jsonbScalarConversion, castCatalogFact{}},
	{JSONB, Float4, Explicit, jsonbScalarConversion, castCatalogFact{}},
	{JSONB, Float8, Explicit, jsonbScalarConversion, castCatalogFact{}},
	{Interval, Time, Assignment, intervalTimeConversion, castCatalogFact{}},
	{Point, Box, Assignment, geometryCastConversion, castCatalogFact{}},
	{Lseg, Point, Explicit, geometryCastConversion, castCatalogFact{}},
	{Path, Polygon, Assignment, geometryCastConversion, castCatalogFact{}},
	{Box, Point, Explicit, geometryCastConversion, castCatalogFact{}},
	{Box, Lseg, Explicit, geometryCastConversion, castCatalogFact{}},
	{Box, Polygon, Assignment, geometryCastConversion, castCatalogFact{}},
	{Box, Circle, Explicit, geometryCastConversion, castCatalogFact{}},
	{Polygon, Point, Explicit, geometryCastConversion, castCatalogFact{}},
	{Polygon, Box, Explicit, geometryCastConversion, castCatalogFact{}},
	{Polygon, Circle, Explicit, geometryCastConversion, castCatalogFact{}},
	{Polygon, Path, Assignment, geometryCastConversion, castCatalogFact{}},
	{Circle, Point, Explicit, geometryCastConversion, castCatalogFact{}},
	{Circle, Box, Explicit, geometryCastConversion, castCatalogFact{}},
	{Circle, Polygon, Explicit, geometryCastConversion, castCatalogFact{}},
}

func castStringCategory(d *Descriptor) bool {
	facts, ok := d.PGTypeMetadata()
	return ok && facts.Category == "S"
}

func sizeCastBits(v Value, typmod int32, explicit bool) (Value, error) {
	if !explicit || typmod < 0 || v.null {
		return sizeBitString(v, typmod)
	}
	n := int(typmod)
	if len(v.text) > n {
		v.text = v.text[:n]
	}
	if v.typ.id == Bit && len(v.text) < n {
		v.text += strings.Repeat("0", n-len(v.text))
	}
	return v, nil
}

func integerCastWidth(id ID) int {
	switch id {
	case Int2:
		return 2
	case Int4:
		return 4
	default:
		return 8
	}
}
func integerCastRange(id ID) error {
	name := "bigint"
	if id == Int2 {
		name = "smallint"
	}
	if id == Int4 {
		name = "integer"
	}
	return failure("22003", name+" out of range")
}

func (p CoercionPlan) applyReleased(ctx context.Context, v Value, input InputEnvironment, output OutputEnvironment) (Value, error) {
	target := p.target.Type
	switch p.method {
	case integerByteaConversion:
		if target.id == Bytea {
			width := integerCastWidth(v.typ.id)
			var bytes [8]byte
			n := uint64(v.integer)
			for i := width - 1; i >= 0; i-- {
				bytes[i] = byte(n)
				n >>= 8
			}
			return Binary(target, string(bytes[:width]))
		}
		width := integerCastWidth(target.id)
		if len(v.text) > width {
			return Value{}, integerCastRange(target.id)
		}
		var n uint64
		for i := range len(v.text) {
			n = n<<8 | uint64(v.text[i])
		}
		if len(v.text) == width && width < 8 {
			n = uint64(int64(n<<(64-width*8)) >> (64 - width*8))
		}
		return Integer(target, int64(n))
	case integerBitConversion:
		if target.id == Bit {
			width := p.target.Typmod
			if width < 0 {
				width = int32(integerCastWidth(v.typ.id) * 8)
			}
			var bits strings.Builder
			bits.Grow(int(width))
			for i := int(width) - 1; i >= 0; i-- {
				if i&1023 == 0 {
					if err := ctx.Err(); err != nil {
						return Value{}, err
					}
				}
				b := byte('0')
				if i >= 64 {
					if v.integer < 0 {
						b = '1'
					}
				} else if uint64(v.integer)>>uint(i)&1 != 0 {
					b = '1'
				}
				bits.WriteByte(b)
			}
			return Value{typ: target, text: bits.String()}, nil
		}
		width := integerCastWidth(target.id) * 8
		if len(v.text) > width {
			return Value{}, integerCastRange(target.id)
		}
		var n uint64
		for i := range len(v.text) {
			n = n<<1 | uint64(v.text[i]-'0')
		}
		if target.id == Int4 {
			return Integer(target, int64(int32(n)))
		}
		return Integer(target, int64(n))
	case internalCharConversion:
		if target.id == Char {
			n := v.integer
			if v.typ.id != Int4 {
				return target.InputWithEnvironment(ctx, v.text, -1, input)
			}
			if n < -128 || n > 127 {
				return Value{}, failure("22003", "\"char\" out of range")
			}
			if n == 0 {
				return String(target, ""), nil
			}
			return String(target, string([]byte{byte(n)})), nil
		}
		if target.id == Int4 {
			n := int64(0)
			if len(v.text) != 0 {
				n = int64(int8(v.text[0]))
			}
			return Integer(target, n)
		}
		if target.id == BPChar {
			text := v.text
			if text == "" {
				text = "\x00"
			}
			return sizeCharacter(String(target, text), p.target.Typmod, p.context == Explicit)
		}
		text, err := v.typ.ConcatOutput(ctx, v, output)
		if err != nil {
			return Value{}, err
		}
		return sizeCharacter(String(target, text), p.target.Typmod, p.context == Explicit)
	case macConversion:
		n := uint64(v.integer)
		if target.id == Macaddr8 {
			n = (n&0xffffff000000)<<16 | 0xfffe000000 | n&0xffffff
		} else {
			if n>>24&0xffff != 0xfffe {
				return Value{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22003", Message: "macaddr8 data out of range to convert to macaddr", Hint: "Only addresses that have FF and FE as values in the 4th and 5th bytes from the left, for example xx:xx:xx:ff:fe:xx:xx:xx, are eligible to be converted from macaddr8 to macaddr."}
			}
			n = n>>16&0xffffff000000 | n&0xffffff
		}
		return Value{typ: target, integer: int64(n)}, nil
	case intervalTimeConversion:
		if intervalInfinity(v) != 0 {
			return Value{}, failure("22008", "cannot convert infinite interval to time")
		}
		n := v.integer % microsPerDay
		if n < 0 {
			n += microsPerDay
		}
		return Value{typ: target, integer: roundTimestamp(n, p.target.Typmod)}, nil
	case jsonbScalarConversion:
		if v.jsonb == nil {
			return Value{}, fmt.Errorf("missing JSONB payload")
		}
		node := v.jsonb.root
		if node.kind == jsonbNull {
			return Null(target), nil
		}
		if target.id == Bool && node.kind == jsonbBoolean {
			return Boolean(target, node.boolean), nil
		}
		if target.id != Bool && node.kind == jsonbNumber {
			number := node.number.value()
			if target.id == Numeric {
				number.typ = target
				return sizeNumeric(number, p.target.Typmod)
			}
			if target.id == Int2 || target.id == Int4 || target.id == Int8 {
				n, err := numericInteger(number, target.id)
				if err != nil {
					return Value{}, err
				}
				return Integer(target, n)
			}
			return target.Input(ctx, numericOutput(number), -1)
		}
		kinds := [...]string{"null", "string", "numeric", "boolean", "array", "object"}
		return Value{}, failure("22023", "cannot cast jsonb "+kinds[node.kind]+" to type "+target.DiagnosticName())
	case geometryCastConversion:
		return p.geometry.Evaluate(ctx, [2]Value{v})
	case rangeMultirangeConversion:
		var members []rangeDatum
		if v.rangeDatum.flags != rangeEmpty {
			members = []rangeDatum{v.rangeDatum}
		}
		return Value{typ: target, multirange: &multirangePayload{members: members}}, nil
	}
	return Value{}, fmt.Errorf("invalid released coercion")
}
