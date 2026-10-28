package index

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/sqltype"
)

// ConstructorColumnStorage predicts one newly constructed index attribute's
// effective policies without allocating or changing either descriptor.
func ConstructorColumnStorage(view catalog.Read, parent, relation *catalog.Relation, slot int) (catalog.ColumnStorage, error) {
	definition := relation.SQLIndex
	attribute := int32(0)
	if slot < len(definition.Keys) {
		attribute = definition.Keys[slot].Attribute
	} else {
		attribute = definition.Include[slot-len(definition.Keys)]
	}
	input := relation.Columns[slot].Usage.Type
	var value catalog.ColumnStorage
	if attribute != 0 {
		parentSlot := parent.IndexForAttribute(attribute)
		if parentSlot < 0 {
			return catalog.ColumnStorage{}, fmt.Errorf("stuntgres: missing index storage parent attribute")
		}
		input = parent.Columns[parentSlot].Usage.Type
		value = parent.ColumnStorage(parentSlot)
	} else {
		value.Storage = input.AttributeFacts().Storage
	}
	if slot < len(definition.Keys) {
		if oid := definition.Keys[slot].Class.StorageOID(); oid != 0 && sqltype.ID(oid) != input.ID() {
			typ, ok := view.LookupType(sqltype.ID(oid))
			if !ok {
				return catalog.ColumnStorage{}, fmt.Errorf("stuntgres: missing index storage type")
			}
			// Only a genuinely different key type resets both policies.
			value = catalog.ColumnStorage{Storage: typ.AttributeFacts().Storage}
		}
	}
	return value, nil
}

// CopyColumnStorage is constructor-only: every simple occurrence is copied.
func CopyColumnStorage(view catalog.Read, parent, relation *catalog.Relation) error {
	for i := range relation.Columns {
		value, err := ConstructorColumnStorage(view, parent, relation, i)
		if err != nil {
			return err
		}
		if value.Storage == relation.Columns[i].Usage.Type.AttributeFacts().Storage {
			value.Storage = ""
		}
		if value == (catalog.ColumnStorage{}) {
			continue
		}
		if relation.ColumnMeta.Storage == nil {
			relation.ColumnMeta.Storage = make(map[int32]catalog.ColumnStorage)
		}
		relation.ColumnMeta.Storage[relation.Attributes[i]] = value
	}
	return nil
}
