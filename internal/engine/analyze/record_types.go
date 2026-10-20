package analyze

import (
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/sqltype"
)

// RecordTypeMapper maps descriptors with leaf, rebuilding the record types
// that contain mapped members. One memoized mapping is shared by a complete tree transformation. Equal OIDs
// never merge distinct record occurrences, including nested anonymous records.
func RecordTypeMapper(leaf func(*sqltype.Descriptor) *sqltype.Descriptor, collations ...typedef.CollationRemapper) func(*sqltype.Descriptor) *sqltype.Descriptor {
	var records map[*sqltype.Descriptor]*sqltype.Descriptor
	var mapType func(*sqltype.Descriptor) *sqltype.Descriptor
	mapType = func(old *sqltype.Descriptor) *sqltype.Descriptor {
		if old.IsDeferredRecord() {
			return old
		}
		if old == nil || !sqltype.OccurrenceRecordArray(old) && (old.ID() != 2249 || !old.IsComposite()) {
			return leaf(old)
		}
		if mapped, ok := records[old]; ok {
			return mapped
		}
		if sqltype.OccurrenceRecordArray(old) {
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
