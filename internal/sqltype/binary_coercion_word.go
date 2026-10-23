package sqltype

import "math"

func binaryOIDAlias(id ID) bool {
	return id == Regclass || id == Regtype || id == Regrole || IsRoutineAlias(id) ||
		IsOperatorAlias(id) || IsRegObjectAlias(id) || IsRegTS(id)
}

func binaryWord32(id ID) bool {
	return id == Int4 || id == Float4 || id == Date || id == Oid || id == Xid || id == Cid || binaryOIDAlias(id)
}

func binaryWord64(id ID) bool {
	switch id {
	case Int8, Float8, Money, Time, Timestamp, Timestamptz, Xid8:
		return true
	}
	return false
}

func binaryEncode32(v Value) (uint32, error) {
	switch v.typ.id {
	case Int4:
		return uint32(v.integer), nil
	case Float4:
		return v.float4Bits(), nil
	case Date:
		switch v.integer {
		case DateNegativeInfinity:
			return 0x80000000, nil
		case DatePositiveInfinity:
			return 0x7fffffff, nil
		}
		return uint32(v.integer), nil
	case Oid, Xid, Cid:
		return v.oid, nil
	default:
		if v.integer != 1 {
			return 0, binaryValueUnsupported()
		}
		return v.oid, nil
	}
}

func binaryDecode32(d *Descriptor, word uint32) (Value, error) {
	v := Value{typ: d}
	switch d.id {
	case Int4:
		v.integer = int64(int32(word))
	case Float4:
		return float4FromBits(d, word), nil
	case Date:
		n := int64(int32(word))
		switch word {
		case 0x80000000:
			n = DateNegativeInfinity
		case 0x7fffffff:
			n = DatePositiveInfinity
		default:
			if n < dateMinimum || n > dateMaximum {
				return Value{}, binaryValueUnsupported()
			}
		}
		v.integer = n
	case Oid, Xid, Cid:
		v.oid = word
	default:
		// REG* aliases are numeric identities. REGCLASS renders this marker
		// lazily; formatting here would allocate a string for every datum.
		v.oid, v.integer = word, 1
	}
	return v, nil
}

func binaryEncode64(v Value) uint64 {
	if v.typ.id == Float8 {
		return math.Float64bits(v.floating)
	}
	return uint64(v.integer)
}

func binaryDecode64(d *Descriptor, word uint64) (Value, error) {
	if d.id == Float8 {
		return Value{typ: d, floating: math.Float64frombits(word)}, nil
	}
	n := int64(word)
	switch d.id {
	case Time:
		if n < 0 || n > microsPerDay {
			return Value{}, binaryValueUnsupported()
		}
	case Timestamp, Timestamptz:
		if n != TimestampNegativeInfinity && n != TimestampPositiveInfinity && (n < timestampMinimum || n > timestampEnd) {
			return Value{}, binaryValueUnsupported()
		}
	}
	return Value{typ: d, integer: n}, nil
}
