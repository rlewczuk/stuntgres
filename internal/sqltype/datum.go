package sqltype

import "math"

// SameDatum compares concrete constant representations, not SQL equality or
// grouping's NOT DISTINCT equivalence. Expression-tree identity must retain
// NUMERIC display scale, BPCHAR bytes and floating signed-zero/NaN bits.
// Usage (including typmod/collation) belongs to the bound expression caller.
func SameDatum(a, b Value) bool {
	if a.typ == nil || a.typ != b.typ || !a.typ.Concrete() {
		return false
	}
	if a.null || b.null {
		return a.null == b.null
	}
	switch a.typ.id {
	case Oid:
		return a.oid == b.oid
	case Bool:
		return a.boolean == b.boolean
	case Int2, Int4, Int8, Date, Time, Timestamp, Timestamptz:
		return a.integer == b.integer
	case Timetz:
		return a.integer == b.integer && a.west == b.west
	case Float4:
		return math.Float32bits(float32(a.floating)) == math.Float32bits(float32(b.floating))
	case Float8:
		return math.Float64bits(a.floating) == math.Float64bits(b.floating)
	case Numeric:
		return a.text == b.text && a.exponent == b.exponent && a.scale == b.scale &&
			a.negative == b.negative && a.special == b.special
	case Text, VarChar, BPChar, Name, Bytea, Regclass, Char:
		return a.text == b.text
	case UUID:
		return a.uuid == b.uuid
	default:
		return false
	}
}
