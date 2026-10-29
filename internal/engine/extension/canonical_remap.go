package extension

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func canonicalDeclarationRemapper(mapping map[ids.ObjectAddress]ids.ObjectAddress) func(catalog.Descriptor) catalog.Descriptor {
	address := func(a ids.ObjectAddress) ids.ObjectAddress {
		if next, ok := mapping[a]; ok {
			return next
		}
		return a
	}
	types := sqltype.LookupIdentityRemapper(func(id sqltype.ID) sqltype.ID {
		return sqltype.ID(address(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(id)}).ObjectID)
	})
	return func(d catalog.Descriptor) catalog.Descriptor {
		if d == nil {
			return nil
		}
		if t, ok := d.(*catalog.Type); ok {
			typ, namespace, extension, relation := types(t.SQLType), address(t.Namespace()), address(t.Extension), address(t.OwningRelation)
			array := sqltype.ID(address(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(t.Array)}).ObjectID)
			if typ == t.SQLType && namespace == t.Namespace() && extension == t.Extension && relation == t.OwningRelation && array == t.Array {
				return t
			}
			n := *t
			n.SQLType = typ
			n.ObjectNamespace = namespace
			n.Extension = extension
			n.OwningRelation = relation
			n.Array = array
			return &n
		}
		// Canonical snapshots have no origin. Their routine/operator/text-search
		// address-bearing declarations share the actual descriptor remap owner.
		return remapDescriptor(d, mapping, func(i *catalog.Installation) *catalog.Installation { return i })
	}
}
