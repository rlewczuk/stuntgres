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

type dumpRegtypeOutput struct{ view CatalogRead }

func (r dumpRegtypeOutput) FormatRegtype(id uint32) (string, error) {
	return regtypeReference(r.view, id), nil
}

func (r scalarRenderer) recordRegtypeReferences(view CatalogRead, value sqltype.Value) error {
	if value.IsNull() || !value.Type().ContainsRegtype() {
		return nil
	}
	if value.Type().BaseType().IsComposite() {
		for i := range value.CompositeLen() {
			if err := r.recordRegtypeReferences(view, value.CompositeField(i)); err != nil {
				return err
			}
		}
		return nil
	}
	if value.Type().BaseType().IsArray() {
		for i := range value.ArrayLen() {
			if err := r.recordRegtypeReferences(view, value.ArrayElement(i)); err != nil {
				return err
			}
		}
		return nil
	}
	id, err := value.TypeOID()
	if err != nil {
		return err
	}
	address := ObjectAddress{ClassID: TypeClassID, ObjectID: OID(id)}
	if _, exists := view.state.descriptors[address]; exists {
		r.record(ObjectRef{owner: view.owner.owner, Epoch: view.Epoch(), Address: address})
	}
	return nil
}

func remapRegtypeDatum(value sqltype.Value, destination func(OID) OID) (sqltype.Value, error) {
	if value.IsNull() || !value.Type().ContainsRegtype() {
		return value, nil
	}
	if value.Type().BaseType().IsComposite() {
		return value.MapComposite(value.Type(), func(member sqltype.Value, _ *sqltype.Descriptor) (sqltype.Value, error) {
			return remapRegtypeDatum(member, destination)
		})
	}
	if value.Type().BaseType().IsArray() {
		return value.MapArray(value.Type(), func(element sqltype.Value, _ *sqltype.Descriptor) (sqltype.Value, error) {
			return remapRegtypeDatum(element, destination)
		})
	}
	id, err := value.TypeOID()
	if err != nil {
		return sqltype.Value{}, err
	}
	target := destination(OID(id))
	if target == OID(id) {
		return value, nil
	}
	physical, err := sqltype.RegtypeValue(value.Type().BaseType(), uint32(target))
	if err != nil {
		return sqltype.Value{}, err
	}
	if value.Type().IsDomain() {
		return value.Type().Wrap(physical)
	}
	return physical, nil
}
