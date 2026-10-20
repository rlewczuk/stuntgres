package catalog

import (
	"iter"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// DefinitionProceduresNamedAt lists the procedures named name in namespace,
// through reader when one is staged and through view otherwise.
func DefinitionProceduresNamedAt(view Read, reader DefinitionReader, namespace ids.ObjectAddress, name string) iter.Seq[*Procedure] {
	if reader != nil {
		return reader.ProceduresNamed(namespace, name)
	}
	return IndexedDefinitionProcedures(view, nil, namespace, name)
}

// Enumeration discovers candidates; the selected provider still supplies the
// ordinary exact dependency and permission proof.
func IndexedDefinitionProcedures(view Read, reader DefinitionReader, namespace ids.ObjectAddress, name string) iter.Seq[*Procedure] {
	return func(yield func(*Procedure) bool) {
		for _, address := range view.State().ProceduresNamed(namespace, name) {
			descriptor, present := DescriptorAt(view, reader, address)
			if d, ok := descriptor.(*Procedure); present && ok && d.Namespace() == namespace && d.Name() == name {
				if !yield(d) {
					return
				}
			}
		}
	}
}

// DefinitionNamespaceAt returns the namespace at address, read through reader
// when one is staged.
func DefinitionNamespaceAt(view Read, reader DefinitionReader, address ids.ObjectAddress) (*Namespace, bool) {
	descriptor, exists := DescriptorAt(view, reader, address)
	namespace, ok := descriptor.(*Namespace)
	return namespace, exists && ok
}

// DefinitionRelationAt returns the relation at address, read through reader
// when one is staged.
func DefinitionRelationAt(view Read, reader DefinitionReader, address ids.ObjectAddress) (*Relation, bool) {
	descriptor, exists := DescriptorAt(view, reader, address)
	relation, ok := descriptor.(*Relation)
	return relation, exists && ok
}

// DefinitionTypeAt returns the SQL type with id, read through reader when one
// is staged.
func DefinitionTypeAt(view Read, reader DefinitionReader, id sqltype.ID) (*sqltype.Descriptor, bool) {
	if reader == nil {
		return view.LookupType(id)
	}
	descriptor, exists := reader.Descriptor(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(id)})
	typ, ok := descriptor.(*Type)
	if !exists || !ok {
		return nil, false
	}
	return typ.SQLType, true
}

// DefinitionArrayAt returns the array type of element, read through reader when
// one is staged.
func DefinitionArrayAt(view Read, reader DefinitionReader, element *sqltype.Descriptor) (*sqltype.Descriptor, bool) {
	if element == nil {
		return nil, false
	}
	if reader == nil {
		return view.LookupArray(element)
	}
	descriptor, exists := reader.Descriptor(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(element.ID())})
	typ, ok := descriptor.(*Type)
	if !exists || !ok || typ.SQLType != element {
		return nil, false
	}
	array, ok := DefinitionTypeAt(view, reader, typ.Array)
	return array, ok && array.IsArray() && array.Element() == element
}

// DefinitionLookupAt resolves key, through reader when one is staged.
func DefinitionLookupAt(view Read, reader DefinitionReader, key ids.NameKey) ids.ObjectAddress {
	if reader != nil {
		return reader.Lookup(key)
	}
	return view.NameAddress(key)
}
