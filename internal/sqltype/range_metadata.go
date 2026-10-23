package sqltype

// RangeMetadata links an executable range to its catalog identities. Companion
// and routine identities alone do not grant execution authority.
// All fields are scalar value copies; the range descriptor owns admission.
type RangeMetadata struct {
	Type, Subtype, Multitype, Collation, Subclass, Canonical, Subdiff uint32
}

func (d *Descriptor) RangeMetadata() (RangeMetadata, bool) {
	if !d.Concrete() || d.IsDomain() || d.rangeSubtype == nil {
		return RangeMetadata{}, false
	}
	if definition, ok := d.RangeDefinition(); ok {
		m := d.multirangeCompanion
		if m == nil || m.multirangeRange != d || !m.Concrete() {
			return RangeMetadata{}, false
		}
		return RangeMetadata{Type: uint32(d.id), Subtype: uint32(d.rangeSubtype.id), Multitype: uint32(m.id), Collation: definition.Subtype.Collation.ID, Subclass: definition.Class.OID(), Subdiff: definition.Diff}, true
	}
	var fact RangeMetadata
	switch d.id {
	case Int4Range:
		fact = RangeMetadata{Type: 3904, Subtype: 23, Multitype: 4451, Subclass: 1978, Canonical: 3914, Subdiff: 3922}
	case NumRange:
		fact = RangeMetadata{Type: 3906, Subtype: 1700, Multitype: 4532, Subclass: 3125, Subdiff: 3924}
	case TimestampRange:
		fact = RangeMetadata{Type: 3908, Subtype: 1114, Multitype: 4533, Subclass: 3128, Subdiff: 3929}
	case TimestamptzRange:
		fact = RangeMetadata{Type: 3910, Subtype: 1184, Multitype: 4534, Subclass: 3127, Subdiff: 3930}
	case DateRange:
		fact = RangeMetadata{Type: 3912, Subtype: 1082, Multitype: 4535, Subclass: 3122, Canonical: 3915, Subdiff: 3925}
	case Int8Range:
		fact = RangeMetadata{Type: 3926, Subtype: 20, Multitype: 4536, Subclass: 3124, Canonical: 3928, Subdiff: 3923}
	default:
		return RangeMetadata{}, false
	}
	if !d.rangeSubtype.Concrete() || d.rangeSubtype.id != ID(fact.Subtype) || d.rangeSubtype.IsDomain() {
		return RangeMetadata{}, false
	}
	if m := d.multirangeCompanion; m != nil && (!m.Concrete() || m.id != ID(fact.Multitype) || m.multirangeRange != d) {
		return RangeMetadata{}, false
	}
	return fact, true
}
