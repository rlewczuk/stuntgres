package sqltype

import (
	"context"
	"math"
)

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
	if a.typ.BaseType().IsEnum() {
		d := a.typ.BaseType()
		_, present := d.enum.ranks[a.integer]
		return present && a.integer == b.integer
	}
	if a.typ.BaseType().IsComposite() {
		if !a.validCompositePayload() || !b.validCompositePayload() {
			return false
		}
		if a.composite == b.composite {
			return true
		}
		for i, field := range a.composite.fields {
			if !SameDatum(field, b.composite.fields[i]) {
				return false
			}
		}
		return true
	}
	if a.typ.BaseType().IsArray() {
		if a.array == nil || b.array == nil {
			return false
		}
		if len(a.array.dimensions) != len(b.array.dimensions) || len(a.array.elements) != len(b.array.elements) {
			return false
		}
		for i, dim := range a.array.dimensions {
			if dim != b.array.dimensions[i] {
				return false
			}
		}
		for i, element := range a.array.elements {
			if !SameDatum(element, b.array.elements[i]) {
				return false
			}
		}
		return true
	}
	switch a.typ.BaseType().id {
	case TSVector, TSQuery:
		if !a.validFTS() || !b.validFTS() {
			return false
		}
		same, err := a.sameFTS(context.Background(), b)
		return err == nil && same
	case ACLItem:
		return a.aclitem.valid() && b.aclitem.valid() && a.aclitem == b.aclitem
	case Macaddr:
		return a.integer >= 0 && uint64(a.integer)>>48 == 0 &&
			b.integer >= 0 && uint64(b.integer)>>48 == 0 && a.integer == b.integer
	case Inet, Cidr:
		return a.network.valid(a.typ.BaseType().id == Cidr) && b.network.valid(b.typ.BaseType().id == Cidr) && a.network == b.network
	case Int4Range, Int8Range, NumRange, DateRange, TimestampRange, TimestamptzRange:
		return a.validRange() && b.validRange() && a.rangeDatum == b.rangeDatum
	case Int4Multirange, Int8Multirange, NumMultirange:
		if !a.validMultirange() || !b.validMultirange() || len(a.multirange.members) != len(b.multirange.members) {
			return false
		}
		if a.multirange == b.multirange {
			return true
		}
		for i, member := range a.multirange.members {
			if member != b.multirange.members[i] {
				return false
			}
		}
		return true
	case JSONPath:
		return a.jsonpath != nil && b.jsonpath != nil && a.text == b.text
	case JSONB:
		if a.jsonb == nil || b.jsonb == nil {
			return false
		}
		n, err := compareJSONB(context.Background(), &a.jsonb.root, &b.jsonb.root, true)
		return err == nil && n == 0
	case PgNodeTree:
		return a.tree == b.tree
	case Interval:
		return a.months == b.months && a.days == b.days && a.integer == b.integer
	case Oid, Xid, Cid:
		return a.oid == b.oid
	case Bool:
		return a.boolean == b.boolean
	case Int2, Int4, Int8, Tid, Xid8, Money, Date, Time, Timestamp, Timestamptz:
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
	case Regtype, Regproc, Regprocedure, Regrole, Regoper, Regoperator, Regcollation, Regnamespace, Regconfig, Regdictionary:
		return a.integer == 1 && b.integer == 1 && a.oid == b.oid
	case Bit, VarBit, Text, VarChar, BPChar, Name, Bytea, Regclass, Char, JSON:
		return a.text == b.text
	case UUID:
		return a.uuid == b.uuid
	default:
		return false
	}
}
