package engine

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// The isolated candidate belongs to a different lifetime. Reserve destination
// enum members and installed base codecs, retaining exact source-pointer import
// authority for the existing nested datum/expression remapping pass. Engine.mu
// is held.
func (e *Engine) importScalarTypesLocked(next *catalogState, mapping map[ids.ObjectAddress]ids.ObjectAddress) error {
	for address, descriptor := range next.descriptors {
		d, ok := descriptor.(*typeDescriptor)
		if !ok || !d.typ.IsEnum() && !d.typ.IsQueryInt() {
			continue
		}
		source := d.typ
		target := address
		if destination, ok := mapping[address]; ok {
			target = destination
		}
		var typ *sqltype.Descriptor
		var err error
		if source.IsEnum() {
			members := source.EnumMembers()
			for i := range members {
				id, er := e.allocateEnumMemberLocked()
				if er != nil {
					return er
				}
				members[i].ID = id
			}
			typ, err = sqltype.EnumWithMembers(sqltype.ID(target.ObjectID), d.Name(), source.SQLName(), members)
		} else {
			typ, err = sqltype.QueryInt(sqltype.ID(target.ObjectID), d.Name(), source.SQLName())
		}
		if err != nil {
			return err
		}
		copy := *d
		copy.typ = typ
		next.descriptors[address] = &copy
		if next.scalarImports == nil {
			next.scalarImports = make(map[*sqltype.Descriptor]*sqltype.Descriptor)
		}
		next.scalarImports[source] = typ
	}
	return nil
}

// Namespace/OID reconstruction may mint a final codec after initial reservation.
// Translation remains keyed by the source pointer and points at that final codec.
func refreshScalarImportDestinations(next *catalogState) {
	for source := range next.scalarImports {
		if d, ok := next.descriptors[ids.ObjectAddress{ClassID: TypeClassID, ObjectID: ids.OID(source.ID())}].(*typeDescriptor); ok {
			next.scalarImports[source] = d.typ
		}
	}
}
