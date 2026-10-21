package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// Dump names ordinary types by their qualified catalog identity. A reciprocal
// associated array uses element[] so replay does not depend on generated names.
func regtypeReference(view catalog.Read, id uint32) string {
	if id == 0 {
		return "-"
	}
	address := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(id)}
	if typ, ok := view.LookupType(sqltype.ID(id)); ok && typ.IsArray() {
		if array, associated := view.LookupArray(typ.Element()); associated && array == typ {
			return regtypeReference(view, uint32(typ.Element().ID())) + "[]"
		}
	}
	if d, ok := view.Find(address); ok {
		return catalog.QuoteIdentifier(view.Get(d.Namespace()).Name()) + "." + catalog.QuoteIdentifier(d.Name())
	}
	return TypeSpelling(view, nil, ids.OID(id))
}

type dumpCatalogAliasOutput struct{ View catalog.Read }

func (r dumpCatalogAliasOutput) FormatRegtype(id uint32) (string, error) {
	return regtypeReference(r.View, id), nil
}
func (r dumpCatalogAliasOutput) FormatRegcollation(id uint32) (string, error) {
	return ObjectSpelling(r.View, nil, ids.CollationClassID, ids.OID(id)), nil
}
func (r dumpCatalogAliasOutput) FormatRegnamespace(id uint32) (string, error) {
	return ObjectSpelling(r.View, nil, ids.NamespaceClassID, ids.OID(id)), nil
}

func RemapAliasDatum(value sqltype.Value, destination func(ids.ObjectAddress) ids.ObjectAddress) (sqltype.Value, error) {
	if value.IsNull() || !value.Type().ContainsRegtype() && !value.Type().ContainsRegObject() {
		return value, nil
	}
	if value.Type().BaseType().IsComposite() {
		return value.MapComposite(value.Type(), func(member sqltype.Value, _ *sqltype.Descriptor) (sqltype.Value, error) {
			return RemapAliasDatum(member, destination)
		})
	}
	if value.Type().BaseType().IsArray() {
		return value.MapArray(value.Type(), func(element sqltype.Value, _ *sqltype.Descriptor) (sqltype.Value, error) {
			return RemapAliasDatum(element, destination)
		})
	}
	address, err := catalog.AliasAddress(value)
	if err != nil {
		return sqltype.Value{}, err
	}
	target := destination(address)
	if target == address {
		return value, nil
	}
	var physical sqltype.Value
	if address.ClassID == ids.TypeClassID {
		physical, err = sqltype.RegtypeValue(value.Type().BaseType(), uint32(target.ObjectID))
	} else {
		physical, err = sqltype.RegObjectValue(value.Type().BaseType(), uint32(target.ObjectID))
	}
	if err != nil {
		return sqltype.Value{}, err
	}
	if value.Type().IsDomain() {
		return value.Type().Wrap(physical)
	}
	return physical, nil
}
