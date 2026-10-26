package typedef

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// ImportScalarTypesLocked gives enum, hstore, query_int, cube, citext and ISN types of
// the isolated candidate next, which belongs to a different lifetime, their
// destination codecs: enum members get fresh identities from
// allocateEnumMember, in member order, and each type is installed at its
// mapped address. The source codecs stay recorded as scalar imports for the
// nested datum and expression remapping pass. allocateEnumMember is the
// engine's enum member allocator; the caller holds Engine.mu for the whole
// call.
func ImportScalarTypesLocked(next *catalog.State, mapping map[ids.ObjectAddress]ids.ObjectAddress, allocateEnumMember func() (int64, error)) error {
	for address, descriptor := range next.Descriptors() {
		d, ok := descriptor.(*catalog.Type)
		if !ok || !d.SQLType.IsEnum() && !d.SQLType.IsHstore() && !d.SQLType.IsQueryInt() && (!d.SQLType.IsLquery() && !d.SQLType.IsLtree()) && !d.SQLType.IsCube() && !d.SQLType.IsCitext() && !d.SQLType.IsEan13() && !d.SQLType.IsIsbn() && !d.SQLType.IsIsbn13() && !d.SQLType.IsIsmn() && !d.SQLType.IsIsmn13() && !d.SQLType.IsIssn() && !d.SQLType.IsIssn13() && !d.SQLType.IsUpc() {
			continue
		}
		source := d.SQLType
		target := address
		if destination, ok := mapping[address]; ok {
			target = destination
		}
		var typ *sqltype.Descriptor
		var err error
		if source.IsEnum() {
			members := source.EnumMembers()
			for i := range members {
				id, er := allocateEnumMember()
				if er != nil {
					return er
				}
				members[i].ID = id
			}
			typ, err = sqltype.EnumWithMembers(sqltype.ID(target.ObjectID), d.Name(), source.SQLName(), members)
		} else if source.IsHstore() {
			typ, err = sqltype.Hstore(sqltype.ID(target.ObjectID), d.Name(), source.SQLName())
		} else if source.IsCube() {
			typ, err = sqltype.Cube(sqltype.ID(target.ObjectID), d.Name(), source.SQLName())
		} else if source.IsCitext() {
			typ, err = sqltype.Citext(sqltype.ID(target.ObjectID), d.Name(), source.SQLName())
		} else if source.IsEan13() {
			typ, err = sqltype.Ean13(sqltype.ID(target.ObjectID), d.Name(), source.SQLName())
		} else if source.IsIsbn() {
			typ, err = sqltype.Isbn(sqltype.ID(target.ObjectID), d.Name(), source.SQLName())
		} else if source.IsIsbn13() {
			typ, err = sqltype.Isbn13(sqltype.ID(target.ObjectID), d.Name(), source.SQLName())
		} else if source.IsIsmn() {
			typ, err = sqltype.Ismn(sqltype.ID(target.ObjectID), d.Name(), source.SQLName())
		} else if source.IsIsmn13() {
			typ, err = sqltype.Ismn13(sqltype.ID(target.ObjectID), d.Name(), source.SQLName())
		} else if source.IsIssn() {
			typ, err = sqltype.Issn(sqltype.ID(target.ObjectID), d.Name(), source.SQLName())
		} else if source.IsIssn13() {
			typ, err = sqltype.Issn13(sqltype.ID(target.ObjectID), d.Name(), source.SQLName())
		} else if source.IsUpc() {
			typ, err = sqltype.Upc(sqltype.ID(target.ObjectID), d.Name(), source.SQLName())
		} else if source.IsLquery() {
			typ, err = sqltype.Lquery(sqltype.ID(target.ObjectID), d.Name(), source.SQLName())
		} else if source.IsLtree() {
			typ, err = sqltype.Ltree(sqltype.ID(target.ObjectID), d.Name(), source.SQLName())
		} else {
			typ, err = sqltype.QueryInt(sqltype.ID(target.ObjectID), d.Name(), source.SQLName())
		}
		if err != nil {
			return err
		}
		copy := *d
		copy.SQLType = typ
		next.SetDescriptor(address, &copy)
		if next.ScalarImports() == nil {
			next.SetScalarImports(make(map[*sqltype.Descriptor]*sqltype.Descriptor))
		}
		next.SetScalarImport(source, typ)
	}
	return nil
}

// RefreshScalarImportDestinations points each scalar import of next at its
// final codec. Namespace and OID reconstruction may mint a final codec after
// the initial reservation; translation stays keyed by the source pointer.
func RefreshScalarImportDestinations(next *catalog.State) {
	for source := range next.ScalarImports() {
		if d, ok := next.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(source.ID())}); ok {
			next.SetScalarImport(source, d.SQLType)
		}
	}
}
