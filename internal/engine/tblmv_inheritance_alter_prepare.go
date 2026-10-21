package engine

import (
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/typedef"
)

func PrepareInheritanceRowType(reader *inheritanceDefinitionReader, relation *catalog.Relation) ([]catalog.Descriptor, error) {
	if relation.RowType == 0 {
		return nil, nil
	}
	descriptor, _ := reader.Descriptor(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(relation.RowType)})
	row, ok := descriptor.(*catalog.Type)
	if !ok {
		return nil, nil
	}
	var effects []catalog.Descriptor
	err := typedef.SynchronizeRowTypeMetadata(relation, row, func(address ids.ObjectAddress) catalog.Descriptor {
		descriptor, _ := reader.Descriptor(address)
		return descriptor
	}, func(namespace ids.ObjectAddress, name string) string {
		return typedef.ChooseAssociatedArrayName(name, func(candidate string) bool {
			return reader.Lookup(ids.NameKey{ClassID: ids.TypeClassID, Namespace: namespace, Name: candidate}) != (ids.ObjectAddress{})
		})
	}, func(descriptor catalog.Descriptor) error {
		reader.AppendDescriptor(descriptor)
		effects = append(effects, descriptor)
		return nil
	})
	return effects, err
}

func DetachPreparedInheritanceDefinition(next *catalog.Relation) *catalog.Relation {
	copy := *next
	copy.Constraints.NotNull = slices.Clone(next.Constraints.NotNull)
	copy.Constraints.Check = slices.Clone(next.Constraints.Check)
	return &copy
}
