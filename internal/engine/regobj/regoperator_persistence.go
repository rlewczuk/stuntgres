package regobj

import (
	"strconv"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/sqltype"
)

// Logical replay preserves all OID bits, including ambiguous names and absent IDs.
type DumpOperatorOutput struct{}

func (DumpOperatorOutput) FormatRegoper(id uint32) (string, error) {
	return strconv.FormatUint(uint64(id), 10), nil
}
func (DumpOperatorOutput) FormatRegoperator(id uint32) (string, error) {
	return strconv.FormatUint(uint64(id), 10), nil
}
func WalkOperatorReferences(view catalog.Read, value sqltype.Value, record func(ids.ObjectAddress) error) error {
	return WalkOperatorReferencesAt(view, nil, value, record)
}

func WalkOperatorReferencesAt(view catalog.Read, reader catalog.DefinitionReader, value sqltype.Value, record func(ids.ObjectAddress) error) error {
	if value.IsNull() || !value.Type().ContainsOperatorAlias() {
		return nil
	}
	if value.Type().BaseType().IsComposite() {
		for i := range value.CompositeLen() {
			if err := WalkOperatorReferencesAt(view, reader, value.CompositeField(i), record); err != nil {
				return err
			}
		}
		return nil
	}
	if value.Type().BaseType().IsArray() {
		for i := range value.ArrayLen() {
			if err := WalkOperatorReferencesAt(view, reader, value.ArrayElement(i), record); err != nil {
				return err
			}
		}
		return nil
	}
	id, err := value.OperatorOID()
	if err != nil {
		return err
	}
	address := ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: ids.OID(id)}
	if _, exists := catalog.DescriptorAt(view, reader, address); exists {
		return record(address)
	}
	return nil
}

// Custom allocated identities replay by exact qualified signature, never OID.
func OperatorLiteral(view catalog.Read, value sqltype.Value) (string, error) {
	id, err := value.OperatorOID()
	if err != nil {
		return "", err
	}
	d, known := view.Operator(ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: ids.OID(id)})
	if !known || routine.OperatorSeeded(d) {
		return "'" + strconv.FormatUint(uint64(id), 10) + "'::" + value.Type().BaseType().Name(), nil
	}
	text := OperatorSpelling(view, nil, CollectOperatorGroups(view), ids.OID(id), sqltype.Regoperator)
	literal := "'" + strings.ReplaceAll(text, "'", "''") + "'::regoperator"
	if value.Type().BaseType().ID() == sqltype.Regoper {
		literal += "::regoper"
	}
	return literal, nil
}
func RemapOperatorDatum(value sqltype.Value, destination func(ids.OID) ids.OID) (sqltype.Value, error) {
	if value.IsNull() || !value.Type().ContainsOperatorAlias() {
		return value, nil
	}
	if value.Type().BaseType().IsComposite() {
		return value.MapComposite(value.Type(), func(member sqltype.Value, _ *sqltype.Descriptor) (sqltype.Value, error) {
			return RemapOperatorDatum(member, destination)
		})
	}
	if value.Type().BaseType().IsArray() {
		return value.MapArray(value.Type(), func(member sqltype.Value, _ *sqltype.Descriptor) (sqltype.Value, error) {
			return RemapOperatorDatum(member, destination)
		})
	}
	id, err := value.OperatorOID()
	if err != nil {
		return sqltype.Value{}, err
	}
	target := destination(ids.OID(id))
	if target == ids.OID(id) {
		return value, nil
	}
	physical, err := sqltype.OperatorValue(value.Type().BaseType(), uint32(target))
	if err != nil {
		return sqltype.Value{}, err
	}
	if value.Type().IsDomain() {
		return value.Type().Wrap(physical)
	}
	return physical, nil
}
