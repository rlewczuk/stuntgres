package engine

import "stuntgres/internal/sqltype"

// Dump names ordinary types by their qualified catalog identity. A reciprocal
// associated array uses element[] so replay does not depend on generated names.
func regtypeReference(view CatalogRead, id uint32) string {
	if id == 0 {
		return "-"
	}
	address := ObjectAddress{ClassID: TypeClassID, ObjectID: OID(id)}
	if typ, ok := view.LookupType(sqltype.ID(id)); ok && typ.IsArray() {
		if array, associated := view.LookupArray(typ.Element()); associated && array == typ {
			return regtypeReference(view, uint32(typ.Element().ID())) + "[]"
		}
	}
	if d, ok := view.state.descriptors[address]; ok {
		return quoteIdentifier(view.state.descriptors[d.Namespace()].Name()) + "." + quoteIdentifier(d.Name())
	}
	return regtypeSpelling(view, nil, OID(id))
}

type dumpCatalogAliasOutput struct{ view CatalogRead }

func (r dumpCatalogAliasOutput) FormatRegtype(id uint32) (string, error) {
	return regtypeReference(r.view, id), nil
}
func (r dumpCatalogAliasOutput) FormatRegcollation(id uint32) (string, error) {
	return regObjectSpelling(r.view, nil, CollationClassID, OID(id)), nil
}
func (r dumpCatalogAliasOutput) FormatRegnamespace(id uint32) (string, error) {
	return regObjectSpelling(r.view, nil, NamespaceClassID, OID(id)), nil
}

func catalogAliasAddress(value sqltype.Value) (ObjectAddress, error) {
	if value.Type().BaseType().ID() == sqltype.Regtype {
		id, err := value.TypeOID()
		return ObjectAddress{ClassID: TypeClassID, ObjectID: OID(id)}, err
	}
	id, err := value.ObjectOID()
	class := NamespaceClassID
	if value.Type().BaseType().ID() == sqltype.Regcollation {
		class = CollationClassID
	}
	return ObjectAddress{ClassID: class, ObjectID: OID(id)}, err
}

// Visit existing represented identities independently of the datum's datatype.
func walkCatalogAliasReferences(view CatalogRead, value sqltype.Value, record func(ObjectAddress) error) error {
	if value.IsNull() || !value.Type().ContainsRegtype() && !value.Type().ContainsRegObject() {
		return nil
	}
	if value.Type().BaseType().IsComposite() {
		for i := range value.CompositeLen() {
			if err := walkCatalogAliasReferences(view, value.CompositeField(i), record); err != nil {
				return err
			}
		}
		return nil
	}
	if value.Type().BaseType().IsArray() {
		for i := range value.ArrayLen() {
			if err := walkCatalogAliasReferences(view, value.ArrayElement(i), record); err != nil {
				return err
			}
		}
		return nil
	}
	address, err := catalogAliasAddress(value)
	if err != nil {
		return err
	}
	if _, exists := view.state.descriptors[address]; exists {
		return record(address)
	}
	return nil
}

// PostgreSQL dependency.c examines the exact Const datatype. The new aliases'
// opaque compound payloads are not scalar Const children. REGTYPE retains its
// existing recursive dependency behavior; replay/remap uses the full walker.
func walkCatalogAliasDependencies(view CatalogRead, value sqltype.Value, record func(ObjectAddress) error) error {
	typ := value.Type()
	// Structural dependency nodes may have declared Usage but no materialized
	// datum. Their datatype edge is recorded by the expression visitor, not here.
	if value.IsNull() || typ == nil {
		return nil
	}
	if sqltype.IsRegObjectAlias(typ.ID()) {
		return walkCatalogAliasReferences(view, value, record)
	}
	if !typ.ContainsRegtype() {
		return nil
	}
	return walkCatalogAliasReferences(view, value, func(address ObjectAddress) error {
		if address.ClassID == TypeClassID {
			return record(address)
		}
		return nil
	})
}

func (r scalarRenderer) recordCatalogAliasDependencies(view CatalogRead, value sqltype.Value) error {
	record := func(address ObjectAddress) error {
		r.record(ObjectRef{owner: view.owner.owner, Epoch: view.Epoch(), Address: address})
		return nil
	}
	if err := walkCatalogAliasDependencies(view, value, record); err != nil {
		return err
	}
	return walkOperatorReferences(view, value, record)
}

func (r scalarRenderer) recordCatalogAliasReferences(view CatalogRead, value sqltype.Value) error {
	record := func(address ObjectAddress) error {
		r.record(ObjectRef{owner: view.owner.owner, Epoch: view.Epoch(), Address: address})
		return nil
	}
	if err := walkCatalogAliasReferences(view, value, record); err != nil {
		return err
	}
	return walkOperatorReferences(view, value, record)
}

func remapCatalogAliasDatum(value sqltype.Value, destination func(ObjectAddress) ObjectAddress) (sqltype.Value, error) {
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
	if address.ClassID == TypeClassID {
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
