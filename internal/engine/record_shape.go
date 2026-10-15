package engine

import "stuntgres/internal/sqltype"

// Anonymous record shapes are occurrence authorities, not catalog providers.
// Every concrete leaf still belongs to the current catalog generation.
func validExpressionUsage(view CatalogRead, usage sqltype.Usage) bool {
	return validExpressionUsageAt(view, nil, usage)
}

func validExpressionUsageAt(view CatalogRead, reader definitionReader, usage sqltype.Usage) bool {
	if !usage.Valid() || validateUsageCollationAt(view, reader, usage) != nil {
		return false
	}
	typ := usage.Type
	if occurrenceRecordArray(typ) {
		return validExpressionUsageAt(view, reader, typ.Element().Usage())
	}
	if typ.ID() == 2249 && typ.IsComposite() {
		for i := range typ.CompositeLen() {
			if !validExpressionUsageAt(view, reader, typ.CompositeMember(i).Usage) {
				return false
			}
		}
		return true
	}
	current, ok := definitionTypeAt(view, reader, typ.ID())
	return ok && current == typ
}

// One memoized mapping is shared by a complete tree transformation. Equal OIDs
// never merge distinct record occurrences, including nested anonymous records.
func recordTypeMapper(leaf func(*sqltype.Descriptor) *sqltype.Descriptor, collations ...collationRemapper) func(*sqltype.Descriptor) *sqltype.Descriptor {
	var records map[*sqltype.Descriptor]*sqltype.Descriptor
	var mapType func(*sqltype.Descriptor) *sqltype.Descriptor
	mapType = func(old *sqltype.Descriptor) *sqltype.Descriptor {
		if old == nil || !occurrenceRecordArray(old) && (old.ID() != 2249 || !old.IsComposite()) {
			return leaf(old)
		}
		if mapped, ok := records[old]; ok {
			return mapped
		}
		if occurrenceRecordArray(old) {
			element := mapType(old.Element())
			mapped := old
			if element != old.Element() {
				var err error
				mapped, err = sqltype.Array(2287, "_record", element)
				if err != nil {
					return nil
				}
			}
			if records == nil {
				records = make(map[*sqltype.Descriptor]*sqltype.Descriptor)
			}
			records[old] = mapped
			return mapped
		}
		var members []sqltype.CompositeMember
		for i := range old.CompositeLen() {
			member := old.CompositeMember(i)
			mapped := mapType(member.Usage.Type)
			mappedCollation := member.Usage.Collation
			if len(collations) != 0 && collations[0] != nil {
				var err error
				mappedCollation, err = collations[0](mappedCollation)
				if err != nil {
					return nil
				}
			}
			if mapped != member.Usage.Type || mappedCollation != member.Usage.Collation {
				if members == nil {
					members = make([]sqltype.CompositeMember, old.CompositeLen())
					for j := range members {
						members[j] = old.CompositeMember(j)
					}
				}
				member.Usage.Type = mapped
				member.Usage.Collation = mappedCollation
				members[i] = member
			}
		}
		mapped := old
		if members != nil {
			var err error
			mapped, err = sqltype.RecordShape(members)
			if err != nil {
				return nil
			}
		}
		if records == nil {
			records = make(map[*sqltype.Descriptor]*sqltype.Descriptor)
		}
		records[old] = mapped
		return mapped
	}
	return mapType
}

func sameRecordUsage(left, right sqltype.Usage) bool {
	if left == right {
		return true
	}
	if occurrenceRecordArray(left.Type) && occurrenceRecordArray(right.Type) {
		return left.Typmod == right.Typmod && left.Collation == right.Collation && sameRecordUsage(left.Type.Element().Usage(), right.Type.Element().Usage())
	}
	if left.Type == nil || right.Type == nil || left.Type.ID() != 2249 || right.Type.ID() != 2249 || !left.Type.IsComposite() || !right.Type.IsComposite() || left.Typmod != right.Typmod || left.Collation != right.Collation || left.Type.CompositeLen() != right.Type.CompositeLen() {
		return false
	}
	for i := range left.Type.CompositeLen() {
		a, b := left.Type.CompositeMember(i), right.Type.CompositeMember(i)
		if a.Attribute != b.Attribute || a.Name != b.Name || !sameRecordUsage(a.Usage, b.Usage) {
			return false
		}
	}
	return true
}

func occurrenceRecordArray(t *sqltype.Descriptor) bool {
	return t != nil && t.ID() == 2287 && t.IsArray() && t.Element().ID() == 2249 && t.Element().IsComposite()
}
