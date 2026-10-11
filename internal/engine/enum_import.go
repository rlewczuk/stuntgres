package engine

import "stuntgres/internal/sqltype"

// Candidate belongs to a different catalog lifetime. Reserve members from the
// destination engine, retaining an exact source-codec translation for the whole
// established nested datum/expression remapping pass. Engine.mu is held.
func (e *Engine) importEnumMembersLocked(next *catalogState, mapping map[ObjectAddress]ObjectAddress) error {
	for address, descriptor := range next.descriptors {
		d, ok := descriptor.(*typeDescriptor)
		if !ok || !d.typ.IsEnum() {
			continue
		}
		source := d.typ
		members := source.EnumMembers()
		for i := range members {
			id, err := e.allocateEnumMemberLocked()
			if err != nil {
				return err
			}
			members[i].ID = id
		}
		target := address
		if destination, ok := mapping[address]; ok {
			target = destination
		}
		typ, err := sqltype.EnumWithMembers(sqltype.ID(target.ObjectID), d.Name(), source.SQLName(), members)
		if err != nil {
			return err
		}
		copy := *d
		copy.typ = typ
		next.descriptors[address] = &copy
		if next.enumImports == nil {
			next.enumImports = make(map[*sqltype.Descriptor]*sqltype.Descriptor)
		}
		next.enumImports[source] = typ
	}
	return nil
}

// Namespace/OID reconstruction may mint a final codec after initial reservation.
// Translation remains keyed by the source pointer and points at that final codec.
func refreshEnumImportDestinations(next *catalogState) {
	for source := range next.enumImports {
		if d, ok := next.descriptors[ObjectAddress{ClassID: TypeClassID, ObjectID: OID(source.ID())}].(*typeDescriptor); ok {
			next.enumImports[source] = d.typ
		}
	}
}
