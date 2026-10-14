package engine

import (
	"strconv"
	"stuntgres/internal/sqltype"
)

// Logical replay preserves all OID bits, including ambiguous names and absent IDs.
type dumpOperatorOutput struct{}

func (dumpOperatorOutput) FormatRegoper(id uint32) (string, error) {
	return strconv.FormatUint(uint64(id), 10), nil
}
func (dumpOperatorOutput) FormatRegoperator(id uint32) (string, error) {
	return strconv.FormatUint(uint64(id), 10), nil
}
func walkOperatorReferences(view CatalogRead, value sqltype.Value, record func(ObjectAddress) error) error {
	if value.IsNull() || !value.Type().ContainsOperatorAlias() {
		return nil
	}
	if value.Type().BaseType().IsComposite() {
		for i := range value.CompositeLen() {
			if err := walkOperatorReferences(view, value.CompositeField(i), record); err != nil {
				return err
			}
		}
		return nil
	}
	if value.Type().BaseType().IsArray() {
		for i := range value.ArrayLen() {
			if err := walkOperatorReferences(view, value.ArrayElement(i), record); err != nil {
				return err
			}
		}
		return nil
	}
	id, err := value.OperatorOID()
	if err != nil {
		return err
	}
	address := ObjectAddress{ClassID: OperatorClassID, ObjectID: OID(id)}
	if _, exists := view.state.descriptors[address]; exists {
		return record(address)
	}
	return nil
}
