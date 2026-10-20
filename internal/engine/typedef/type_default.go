package typedef

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func TypeDefault(view catalog.Read, typ *sqltype.Descriptor) *catalog.ColumnDefault {
	if !typ.IsDomain() {
		return nil
	}
	d, ok := view.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(typ.ID())})
	if !ok || d.SQLType != typ {
		return nil
	}
	return d.DomainDefault
}
