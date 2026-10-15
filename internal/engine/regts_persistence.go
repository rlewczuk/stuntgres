package engine

import "stuntgres/internal/sqltype"

type dumpRegTSOutput struct{ view CatalogRead }

func (r dumpRegTSOutput) FormatRegconfig(id uint32) (string, error) {
	return regTSSpelling(r.view, nil, id, sqltype.Regconfig, true), nil
}
func (r dumpRegTSOutput) FormatRegdictionary(id uint32) (string, error) {
	return regTSSpelling(r.view, nil, id, sqltype.Regdictionary, true), nil
}

func walkRegTSReferences(view CatalogRead, value sqltype.Value, record func(ObjectAddress) error) error {
	return walkRegTSReferencesAt(view, nil, value, record)
}

func walkRegTSReferencesAt(view CatalogRead, reader definitionReader, value sqltype.Value, record func(ObjectAddress) error) error {
	if value.IsNull() || !value.Type().ContainsRegTS() {
		return nil
	}
	if value.Type().BaseType().IsComposite() {
		for i := range value.CompositeLen() {
			if err := walkRegTSReferencesAt(view, reader, value.CompositeField(i), record); err != nil {
				return err
			}
		}
		return nil
	}
	if value.Type().BaseType().IsArray() {
		for i := range value.ArrayLen() {
			if err := walkRegTSReferencesAt(view, reader, value.ArrayElement(i), record); err != nil {
				return err
			}
		}
		return nil
	}
	if value.Type().BaseType().ID() != sqltype.Regdictionary {
		return nil
	}
	id, err := value.RegTSOID()
	if err != nil {
		return err
	}
	address := ObjectAddress{ClassID: TSDictionaryClassID, ObjectID: OID(id)}
	descriptor, _ := definitionDescriptorAt(view, reader, address)
	if _, ok := descriptor.(*tsDictionaryDescriptor); ok {
		return record(address)
	}
	return nil
}
func remapRegTSDatum(value sqltype.Value, destination func(ObjectAddress) ObjectAddress) (sqltype.Value, error) {
	if value.IsNull() || !value.Type().ContainsRegTS() {
		return value, nil
	}
	if value.Type().BaseType().IsComposite() {
		return value.MapComposite(value.Type(), func(member sqltype.Value, _ *sqltype.Descriptor) (sqltype.Value, error) {
			return remapRegTSDatum(member, destination)
		})
	}
	if value.Type().BaseType().IsArray() {
		return value.MapArray(value.Type(), func(member sqltype.Value, _ *sqltype.Descriptor) (sqltype.Value, error) {
			return remapRegTSDatum(member, destination)
		})
	}
	if value.Type().BaseType().ID() != sqltype.Regdictionary {
		return value, nil
	}
	id, err := value.RegTSOID()
	if err != nil {
		return sqltype.Value{}, err
	}
	if id == 0 {
		return value, nil
	}
	address := ObjectAddress{ClassID: TSDictionaryClassID, ObjectID: OID(id)}
	target := destination(address)
	if target == address {
		return value, nil
	}
	physical, err := sqltype.RegTSValue(value.Type().BaseType(), uint32(target.ObjectID))
	if err != nil {
		return sqltype.Value{}, err
	}
	if value.Type().IsDomain() {
		return value.Type().Wrap(physical)
	}
	return physical, nil
}
