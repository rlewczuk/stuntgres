package sqltype

// PostgreSQLAttributeFacts is the released 64-bit pg_type storage contract,
// independent of native value representation or codec executability.
type PostgreSQLAttributeFacts struct {
	Length             int16
	ByValue            bool
	Alignment, Storage string
}

// AttributeFacts follows REL_18_0 pg_type.dat and genbki's generated arrays.
// Domains retain their identity but inherit these physical facts from the base.
func (d *Descriptor) AttributeFacts() PostgreSQLAttributeFacts {
	if d.IsDomain() {
		return d.base.Type.AttributeFacts()
	}
	f := PostgreSQLAttributeFacts{Length: d.PostgreSQLTypeLength(), Alignment: "i", Storage: "p"}
	if d.IsCatalogVector() { return f }
	if d.IsArray() {
		f.Alignment, f.Storage = "i", "x"
		if d.element.AttributeFacts().Alignment == "d" {
			f.Alignment = "d"
		}
		return f
	}
	if d.IsComposite() {
		f.Alignment, f.Storage = "d", "x"
		return f
	}
	if d.IsEnum() {
		f.ByValue = true
		return f
	}
	if d.IsRange() || d.IsMultirange() {
		f.Storage = "x"
		if d.RangeType().rangeSubtype.AttributeFacts().Alignment == "d" {
			f.Alignment = "d"
		}
		return f
	}
	if d.IsCube() {
		f.Alignment = "d"
		return f
	}
	if d.IsCitext() {
		f.Storage = "x"
		return f
	}
	if d.IsEan13() || d.IsIsbn() || d.IsIsbn13() || d.IsIsmn() || d.IsIsmn13() || d.IsIssn() || d.IsIssn13() || d.IsUpc() {
		f.ByValue, f.Alignment = true, "d"
		return f
	}
	switch d.id {
	case Bool, Char:
		f.ByValue, f.Alignment = true, "c"
	case Name, UUID, Unknown, 2275:
		f.Alignment = "c"
	case Int2:
		f.ByValue, f.Alignment = true, "s"
	case 27:
		f.Alignment = "s"
	case Int4, Oid, Xid, Cid, Date, Float4, Regproc, Regclass, Regtype, Regrole, Regoper, Regoperator, Void,
		269, 325, 2202, 2276, 2279, 2280, 2283, 2776, 3115, 3310, 3500, 3734, 3769, 3838, 4089, 4191, 5077, 5079:
		f.ByValue = true
	case Int8, Float8, Money, Time, Timestamp, Timestamptz, Xid8, 32, 2281, 3220:
		f.ByValue, f.Alignment = true, "d"
	case Interval, Timetz, ACLItem, 600, 601, 603, 628, 718:
		f.Alignment = "d"
	case Numeric, Inet, Cidr:
		f.Storage = "m"
	case Bytea, Text, BPChar, VarChar, Bit, VarBit, JSON, JSONB, JSONPath, PgNodeTree, 142, 1790, 3361, 3402, 5017, 3614, 4600, 4601:
		f.Storage = "x"
	case 4535:
		f.Storage = "x"
	case 4533, 4534, 602, 604, 2970, 5038,
		71, 75, 81, 83, 2249, 2277, 3831, 5078, 5080, 4537, 4538:
		f.Alignment, f.Storage = "d", "x"
	}
	return f
}

// CatalogNullUsage permits only the canonical builtin ANYARRAY description.
// LookupOnly, domains and arrays cannot manufacture this authority. This does
// not make the pseudo type valid for input, storage, parameters or operators.
func (u Usage) CatalogNullUsage() bool {
	d := u.Type
	return d != nil && d.catalogNullOwner == d && d.id == 2277 && d.namespace == "pg_catalog" && d.name == "anyarray" && d.lookupOnly && d.base.Type == nil && d.element == nil && u.Typmod == -1 && u.Collation == (CollationRef{})
}

func CatalogResultNull(u Usage) Value {
	if !u.CatalogNullUsage() {
		panic("invalid catalog NULL usage")
	}
	return Value{typ: u.Type, null: true}
}

// ResultNull is query padding, not declared-type admission. Ordinary NULL's
// concrete-only invariant remains unchanged for codecs and stored definitions.
func ResultNull(u Usage) Value {
	if u.CatalogNullUsage() {
		return CatalogResultNull(u)
	}
	return Null(u.Type)
}

// ValidResult preserves exact captured descriptor identity, including the
// narrowly nullable catalog carrier. No nonnull pseudo value can escape.
func (v Value) ValidResult(u Usage) bool {
	if v.typ!=nil && !v.null && v.typ.BaseType().IsCatalogVector() && !v.validCatalogVectorPayload() { return false }
	return v.typ != nil && v.typ == u.Type && (v.typ.Concrete() || v.null && u.CatalogNullUsage()) && (v.null || (!v.typ.BaseType().IsEan13() || v.validEan13()) && (!v.typ.BaseType().IsIsbn() || v.validIsbn()) && (!v.typ.BaseType().IsIsbn13() || v.validIsbn13()) && (!v.typ.BaseType().IsIsmn() || v.validIsmn()) && (!v.typ.BaseType().IsIsmn13() || v.validIsmn13()) && (!v.typ.BaseType().IsIssn() || v.validIssn()) && (!v.typ.BaseType().IsIssn13() || v.validIssn13()) && (!v.typ.BaseType().IsUpc() || v.validUpc()))
}
