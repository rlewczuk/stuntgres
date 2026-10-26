package regobj

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

type DumpTextSearchOutput struct{ View catalog.Read }

func (r DumpTextSearchOutput) FormatRegconfig(id uint32) (string, error) {
	return TextSearchSpelling(r.View, nil, id, sqltype.Regconfig, true), nil
}
func (r DumpTextSearchOutput) FormatRegdictionary(id uint32) (string, error) {
	return TextSearchSpelling(r.View, nil, id, sqltype.Regdictionary, true), nil
}

func WalkTextSearchReferences(view catalog.Read, value sqltype.Value, record func(ids.ObjectAddress) error) error {
	return WalkTextSearchReferencesAt(view, nil, value, record)
}

func WalkTextSearchReferencesAt(view catalog.Read, reader catalog.DefinitionReader, value sqltype.Value, record func(ids.ObjectAddress) error) error {
	if value.IsNull() || !value.Type().ContainsRegTS() {
		return nil
	}
	if value.Type().BaseType().RangeType() != nil {
		return value.VisitRangeEndpoints(func(leaf sqltype.Value) error { return WalkTextSearchReferencesAt(view, reader, leaf, record) })
	}
	if value.Type().BaseType().IsComposite() {
		for i := range value.CompositeLen() {
			if err := WalkTextSearchReferencesAt(view, reader, value.CompositeField(i), record); err != nil {
				return err
			}
		}
		return nil
	}
	if value.Type().BaseType().IsArray() {
		for i := range value.ArrayLen() {
			if err := WalkTextSearchReferencesAt(view, reader, value.ArrayElement(i), record); err != nil {
				return err
			}
		}
		return nil
	}
	if value.Type().BaseType().ID() != sqltype.Regdictionary && value.Type().BaseType().ID() != sqltype.Regconfig {
		return nil
	}
	id, err := value.RegTSOID()
	if err != nil {
		return err
	}
	class := ids.TSDictionaryClassID
	if value.Type().BaseType().ID() == sqltype.Regconfig {
		class = ids.TSConfigurationClassID
	}
	address := ids.ObjectAddress{ClassID: class, ObjectID: ids.OID(id)}
	descriptor, _ := catalog.DescriptorAt(view, reader, address)
	if descriptor != nil && descriptor.Address().ClassID == class {
		return record(address)
	}
	return nil
}
func RemapTextSearchDatum(value sqltype.Value, destination func(ids.ObjectAddress) ids.ObjectAddress) (sqltype.Value, error) {
	if value.IsNull() || !value.Type().ContainsRegTS() {
		return value, nil
	}
	if value.Type().BaseType().RangeType() != nil {
		return value.MapRange(value.Type(), sqltype.RangeMapIdentity, func(leaf sqltype.Value, _ *sqltype.Descriptor) (sqltype.Value, error) {
			return RemapTextSearchDatum(leaf, destination)
		})
	}
	if value.Type().BaseType().IsComposite() {
		return value.MapComposite(value.Type(), func(member sqltype.Value, _ *sqltype.Descriptor) (sqltype.Value, error) {
			return RemapTextSearchDatum(member, destination)
		})
	}
	if value.Type().BaseType().IsArray() {
		return value.MapArray(value.Type(), func(member sqltype.Value, _ *sqltype.Descriptor) (sqltype.Value, error) {
			return RemapTextSearchDatum(member, destination)
		})
	}
	if value.Type().BaseType().ID() != sqltype.Regdictionary && value.Type().BaseType().ID() != sqltype.Regconfig {
		return value, nil
	}
	id, err := value.RegTSOID()
	if err != nil {
		return sqltype.Value{}, err
	}
	if id == 0 {
		return value, nil
	}
	class := ids.TSDictionaryClassID
	if value.Type().BaseType().ID() == sqltype.Regconfig {
		class = ids.TSConfigurationClassID
	}
	address := ids.ObjectAddress{ClassID: class, ObjectID: ids.OID(id)}
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
