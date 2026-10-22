package sqltype

// ComparisonCapabilities reports exact default datum-provider capabilities, not
// whether an implicit scalar operator can be selected for the source type.
// Structural consumers refine their reached-field demand independently.
func ComparisonCapabilities(d *Descriptor) (equality, ordering bool) {
	if d == nil || !d.Concrete() {
		return false, false
	}
	d = d.BaseType()
	if d.IsEnum() || d.IsComposite() {
		return true, true
	}
	if d.IsArray() {
		return ComparisonCapabilities(d.Element())
	}
	if isRange(d.ID()) || isMultirange(d.ID()) {
		return true, true
	}
	if d.ID() == PgNodeTree {
		// typcache uses the binary-compatible TEXT default B-tree class,
		// not a nonexistent pg_node_tree SQL operator signature.
		class, ok := DefaultIndexClass(d)
		return ok && class.input == Text, ok && class.input == Text
	}
	switch d.ID() {
	case ACLItem, Cid, Xid:
		return true, false
	case Bool, Bytea, Char, Name, Int2, Int4, Int8, Float4, Float8,
		Numeric, Money, Text, BPChar, VarChar, Bit, VarBit,
		Date, Time, Timetz, Timestamp, Timestamptz, Interval,
		Oid, Tid, Macaddr, Xid8, UUID, Inet, Cidr, JSONB,
		Regclass, Regtype, Regrole, Regproc, Regprocedure,
		Regoper, Regoperator, Regcollation, Regnamespace, Regconfig, Regdictionary:
		return true, true
	}
	return false, false
}
