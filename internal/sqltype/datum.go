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
	if a.typ != nil && a.typ == b.typ && a.typ.ID() == 32 && a.typ.Usage().QueryNullUsage() {
		if !a.ValidResult(a.typ.Usage()) || !b.ValidResult(b.typ.Usage()) {
			return false
		}
		if a.null || b.null {
			return a.null == b.null
		}
		return a.ddlCommand.command == b.ddlCommand.command
	}
	if a.typ == nil || a.typ != b.typ || !a.typ.Concrete() && !a.typ.Usage().CatalogNullUsage() {
		return false
	}
	if a.null || b.null {
		return a.null == b.null
	}
	if a.typ.Usage().CatalogNullUsage() {
		if !a.validStatisticResult() || !b.validStatisticResult() {
			return false
		}
		same, err := a.SamePayload(context.Background(), b)
		return err == nil && same
	}
	if IsStatisticsType(a.typ.BaseType().id) {
		return a.validStatistics() && b.validStatistics() && *a.statistics == *b.statistics
	}
	if a.typ.BaseType().IsSeg() {
		return a.seg.valid() && b.seg.valid() && a.seg == b.seg
	}
	if a.typ.BaseType().IsLquery() {
		return a.validLquery() && b.validLquery() && a.lquery.text == b.lquery.text
	}
	if a.typ.BaseType().IsLtxtquery() {
		return a.validLtxtquery() && b.validLtxtquery() && a.ltxtquery.text == b.ltxtquery.text
	}
	if a.typ.BaseType().IsLtree() {
		return a.validLtree() && b.validLtree() && a.ltree.text == b.ltree.text
	}
	if a.typ.BaseType().IsHstore() {
		if !a.validHstore() || !b.validHstore() || len(a.hstore.pairs) != len(b.hstore.pairs) {
			return false
		}
		for i, pair := range a.hstore.pairs {
			if pair != b.hstore.pairs[i] {
				return false
			}
		}
		return true
	}
	if a.typ.BaseType().IsCube() {
		if !a.validCube() || !b.validCube() {
			return false
		}
		if a.cube == b.cube {
			return true
		}
		if a.cube.dimension != b.cube.dimension || a.cube.point != b.cube.point {
			return false
		}
		for i, coordinate := range a.cube.coordinates {
			if math.Float64bits(coordinate) != math.Float64bits(b.cube.coordinates[i]) {
				return false
			}
		}
		return true
	}
	if a.typ.BaseType().IsCitext() {
		return a.text == b.text
	}
	if a.typ.BaseType().IsEan13() {
		return a.validEan13() && b.validEan13() && a.integer == b.integer
	}
	if a.typ.BaseType().IsIsbn() {
		return a.validIsbn() && b.validIsbn() && a.integer == b.integer
	}
	if a.typ.BaseType().IsIsbn13() {
		return a.validIsbn13() && b.validIsbn13() && a.integer == b.integer
	}
	if a.typ.BaseType().IsIsmn() {
		return a.validIsmn() && b.validIsmn() && a.integer == b.integer
	}
	if a.typ.BaseType().IsIsmn13() {
		return a.validIsmn13() && b.validIsmn13() && a.integer == b.integer
	}
	if a.typ.BaseType().IsIssn() {
		return a.validIssn() && b.validIssn() && a.integer == b.integer
	}
	if a.typ.BaseType().IsIssn13() {
		return a.validIssn13() && b.validIssn13() && a.integer == b.integer
	}
	if a.typ.BaseType().IsUpc() {
		return a.validUpc() && b.validUpc() && a.integer == b.integer
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
	if a.typ.BaseType().IsRange() {
		return a.validRange() && b.validRange() && sameRangeDatum(a.rangeDatum, b.rangeDatum)
	}
	if a.typ.BaseType().IsMultirange() {
		valid := func(v Value) bool {
			if v.typ.BaseType().RangeType().customRange != nil {
				return v.validMultirangeShape()
			}
			return v.validMultirange()
		}
		if !valid(a) || !valid(b) || len(a.multirange.members) != len(b.multirange.members) {
			return false
		}
		if a.multirange == b.multirange {
			return true
		}
		for i, member := range a.multirange.members {
			if !sameRangeDatum(member, b.multirange.members[i]) {
				return false
			}
		}
		return true
	}
	switch a.typ.BaseType().id {
	case Point:
		return math.Float64bits(a.floating) == math.Float64bits(b.floating) && a.integer == b.integer
	case Box, Line, Lseg:
		return a.geometry != nil && b.geometry != nil && a.sameGeometry(b)
	case Path:
		return samePath(a, b)
	case Polygon:
		return a.samePolygon(b)
	case Circle:
		return math.Float64bits(a.circle.x) == math.Float64bits(b.circle.x) &&
			math.Float64bits(a.circle.y) == math.Float64bits(b.circle.y) &&
			math.Float64bits(a.circle.radius) == math.Float64bits(b.circle.radius)
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
	case Macaddr8:
		return a.integer == b.integer
	case Inet, Cidr:
		return a.network.valid(a.typ.BaseType().id == Cidr) && b.network.valid(b.typ.BaseType().id == Cidr) && a.network == b.network
	case PgSnapshot, TxidSnapshot:
		if a.snapshot == nil || b.snapshot == nil {
			return false
		}
		if a.snapshot == b.snapshot {
			return true
		}
		if a.snapshot.xmin != b.snapshot.xmin || a.snapshot.xmax != b.snapshot.xmax || len(a.snapshot.members) != len(b.snapshot.members) {
			return false
		}
		for i, id := range a.snapshot.members {
			if id != b.snapshot.members[i] {
				return false
			}
		}
		return true
	case XML:
		return a.validXML() && b.validXML() && a.xml == b.xml
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
	case Int2, Int4, Int8, Tid, Xid8, PgLSN, Money, Date, Time, Timestamp, Timestamptz:
		return a.integer == b.integer
	case Timetz:
		return a.integer == b.integer && a.west == b.west
	case Float4:
		return a.float4Bits() == b.float4Bits()
	case Float8:
		return math.Float64bits(a.floating) == math.Float64bits(b.floating)
	case Numeric:
		return a.text == b.text && a.exponent == b.exponent && a.scale == b.scale &&
			a.negative == b.negative && a.special == b.special
	case Regtype, Regproc, Regprocedure, Regrole, Regoper, Regoperator, Regcollation, Regnamespace, Regconfig, Regdictionary:
		return a.integer == 1 && b.integer == 1 && a.oid == b.oid
	case Regclass:
		if a.integer == 1 || b.integer == 1 {
			return a.integer == 1 && b.integer == 1 && a.oid == b.oid
		}
		return a.text == b.text
	case Refcursor, Bit, VarBit, Text, VarChar, BPChar, Name, Bytea, Char, JSON, 2275:
		return a.text == b.text
	case UUID:
		return a.uuid == b.uuid
	default:
		return false
	}
}

func sameRangeDatum(a, b rangeDatum) bool {
	if a.flags != b.flags {
		return false
	}
	bound := func(a, b rangeBound) bool {
		if a.value == nil || b.value == nil {
			return a == b
		}
		return SameDatum(*a.value, *b.value)
	}
	return bound(a.lower, b.lower) && bound(a.upper, b.upper)
}
