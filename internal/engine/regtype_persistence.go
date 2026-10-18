package engine

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// Dump names ordinary types by their qualified catalog identity. A reciprocal
// associated array uses element[] so replay does not depend on generated names.
func regtypeReference(view CatalogRead, id uint32) string {
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
		return quoteIdentifier(view.Get(d.Namespace()).Name()) + "." + quoteIdentifier(d.Name())
	}
	return regtypeSpelling(view, nil, ids.OID(id))
}

type dumpCatalogAliasOutput struct{ view CatalogRead }

func (r dumpCatalogAliasOutput) FormatRegtype(id uint32) (string, error) {
	return regtypeReference(r.view, id), nil
}
func (r dumpCatalogAliasOutput) FormatRegcollation(id uint32) (string, error) {
	return regObjectSpelling(r.view, nil, ids.CollationClassID, ids.OID(id)), nil
}
func (r dumpCatalogAliasOutput) FormatRegnamespace(id uint32) (string, error) {
	return regObjectSpelling(r.view, nil, ids.NamespaceClassID, ids.OID(id)), nil
}

func catalogAliasAddress(value sqltype.Value) (ids.ObjectAddress, error) {
	if value.Type().BaseType().ID() == sqltype.Regtype {
		id, err := value.TypeOID()
		return ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(id)}, err
	}
	id, err := value.ObjectOID()
	class := ids.NamespaceClassID
	if value.Type().BaseType().ID() == sqltype.Regcollation {
		class = ids.CollationClassID
	}
	return ids.ObjectAddress{ClassID: class, ObjectID: ids.OID(id)}, err
}

// Visit existing represented identities independently of the datum's datatype.
func walkCatalogAliasReferences(view CatalogRead, value sqltype.Value, record func(ids.ObjectAddress) error) error {
	return walkCatalogAliasReferencesAt(view, nil, value, record)
}

func walkCatalogAliasReferencesAt(view CatalogRead, reader definitionReader, value sqltype.Value, record func(ids.ObjectAddress) error) error {
	if value.IsNull() || !value.Type().ContainsRegtype() && !value.Type().ContainsRegObject() {
		return nil
	}
	if value.Type().BaseType().IsComposite() {
		for i := range value.CompositeLen() {
			if err := walkCatalogAliasReferencesAt(view, reader, value.CompositeField(i), record); err != nil {
				return err
			}
		}
		return nil
	}
	if value.Type().BaseType().IsArray() {
		for i := range value.ArrayLen() {
			if err := walkCatalogAliasReferencesAt(view, reader, value.ArrayElement(i), record); err != nil {
				return err
			}
		}
		return nil
	}
	address, err := catalogAliasAddress(value)
	if err != nil {
		return err
	}
	if _, exists := definitionDescriptorAt(view, reader, address); exists {
		return record(address)
	}
	return nil
}

// PostgreSQL dependency.c examines the exact Const datatype. The new aliases'
// opaque compound payloads are not scalar Const children. REGTYPE retains its
// existing recursive dependency behavior; replay/remap uses the full walker.
func walkCatalogAliasDependencies(view CatalogRead, value sqltype.Value, record func(ids.ObjectAddress) error) error {
	return walkCatalogAliasDependenciesAt(view, nil, value, record)
}

func walkCatalogAliasDependenciesAt(view CatalogRead, reader definitionReader, value sqltype.Value, record func(ids.ObjectAddress) error) error {
	typ := value.Type()
	// Structural dependency nodes may have declared Usage but no materialized
	// datum. Their datatype edge is recorded by the expression visitor, not here.
	if value.IsNull() || typ == nil {
		return nil
	}
	if sqltype.IsRegObjectAlias(typ.ID()) {
		return walkCatalogAliasReferencesAt(view, reader, value, record)
	}
	if !typ.ContainsRegtype() {
		return nil
	}
	return walkCatalogAliasReferencesAt(view, reader, value, func(address ids.ObjectAddress) error {
		if address.ClassID == ids.TypeClassID {
			return record(address)
		}
		return nil
	})
}

func (r scalarRenderer) recordCatalogAliasDependencies(view CatalogRead, value sqltype.Value) error {
	record := func(address ids.ObjectAddress) error {
		r.record(view.owner.Domain.Ref(view.Epoch(), address))
		return nil
	}
	if err := walkCatalogAliasDependenciesAt(view, r.definition, value, record); err != nil {
		return err
	}
	if err := walkOperatorReferencesAt(view, r.definition, value, record); err != nil {
		return err
	}
	return walkRegTSReferencesAt(view, r.definition, value, record)
}

func (r scalarRenderer) recordCatalogAliasReferences(view CatalogRead, value sqltype.Value) error {
	record := func(address ids.ObjectAddress) error {
		r.record(view.owner.Domain.Ref(view.Epoch(), address))
		return nil
	}
	if err := walkCatalogAliasReferencesAt(view, r.definition, value, record); err != nil {
		return err
	}
	if err := walkOperatorReferencesAt(view, r.definition, value, record); err != nil {
		return err
	}
	return walkRegTSReferencesAt(view, r.definition, value, record)
}

func remapCatalogAliasDatum(value sqltype.Value, destination func(ids.ObjectAddress) ids.ObjectAddress) (sqltype.Value, error) {
	if value.IsNull() || !value.Type().ContainsRegtype() && !value.Type().ContainsRegObject() {
		return value, nil
	}
	if value.Type().BaseType().IsComposite() {
		return value.MapComposite(value.Type(), func(member sqltype.Value, _ *sqltype.Descriptor) (sqltype.Value, error) {
			return remapCatalogAliasDatum(member, destination)
		})
	}
	if value.Type().BaseType().IsArray() {
		return value.MapArray(value.Type(), func(element sqltype.Value, _ *sqltype.Descriptor) (sqltype.Value, error) {
			return remapCatalogAliasDatum(element, destination)
		})
	}
	address, err := catalogAliasAddress(value)
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
