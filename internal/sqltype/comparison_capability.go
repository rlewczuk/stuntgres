package sqltype

// ComparisonCapabilities reports exact default datum-provider capabilities, not
// whether an implicit scalar operator can be selected for the source type.
// Structural consumers refine their reached-field demand independently.
func ComparisonCapabilities(d *Descriptor) (equality, ordering bool) {
	if d == nil || !d.Concrete() {
		return false, false
	}
	d = d.BaseType()
	if d.IsEnum() || d.IsComposite() || d.IsCube() {
		return true, true
	}
	if d.IsArray() {
		return ComparisonCapabilities(d.Element())
	}
	if isRange(d.ID()) || isMultirange(d.ID()) {
		return true, true
	}
	if d.ID() == PgNodeTree {
		// The datum provider is binary-compatible with TEXT. This is a type
		// capability, not a lookup of the current catalog's default class.
		return true, true
	}
	switch d.ID() {
	case ACLItem, Cid, Xid:
		return true, false
	case Bool, Bytea, Char, Name, Int2, Int4, Int8, Float4, Float8,
		Numeric, Money, Text, BPChar, VarChar, Bit, VarBit,
		Date, Time, Timetz, Timestamp, Timestamptz, Interval,
		Oid, Tid, Macaddr, Macaddr8, Xid8, UUID, Inet, Cidr, JSONB,
		Regclass, Regtype, Regrole, Regproc, Regprocedure,
		Regoper, Regoperator, Regcollation, Regnamespace, Regconfig, Regdictionary:
		return true, true
	}
	return false, false
}
