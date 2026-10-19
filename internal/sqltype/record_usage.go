package sqltype

// SameRecordUsage reports whether two usages describe the same record shape:
// equal usages, two deferred records, record arrays over the same shape, or
// anonymous composite records with the same members.
func SameRecordUsage(left, right Usage) bool {
	if left == right {
		return true
	}
	if left.Type.IsDeferredRecord() && right.Type.IsDeferredRecord() && left.Typmod == right.Typmod && left.Collation == right.Collation {
		return true
	}
	if OccurrenceRecordArray(left.Type) && OccurrenceRecordArray(right.Type) {
		return left.Typmod == right.Typmod && left.Collation == right.Collation && SameRecordUsage(left.Type.Element().Usage(), right.Type.Element().Usage())
	}
	if left.Type == nil || right.Type == nil || left.Type.ID() != 2249 || right.Type.ID() != 2249 || !left.Type.IsComposite() || !right.Type.IsComposite() || left.Typmod != right.Typmod || left.Collation != right.Collation || left.Type.CompositeLen() != right.Type.CompositeLen() {
		return false
	}
	for i := range left.Type.CompositeLen() {
		a, b := left.Type.CompositeMember(i), right.Type.CompositeMember(i)
		if a.Attribute != b.Attribute || a.Name != b.Name || !SameRecordUsage(a.Usage, b.Usage) {
			return false
		}
	}
	return true
}

// OccurrenceRecordArray reports whether t is a record[] over an anonymous
// composite record.
func OccurrenceRecordArray(t *Descriptor) bool {
	return t != nil && t.ID() == 2287 && t.IsArray() && t.Element().ID() == 2249 && t.Element().IsComposite()
}
