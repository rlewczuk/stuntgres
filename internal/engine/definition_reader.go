package engine

import (
	"iter"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// Definition cooking alone can read the exact ordered, unpublished DROP effects.
// This is not another catalog generation, SQL execution route or permission
// cache. Bound definitions retain their normal identities/provider observations;
// the reader is scoped to preparation/consumption and is never persisted.
type definitionReader interface {
	descriptor(ids.ObjectAddress) (Descriptor, bool)
	lookup(ids.NameKey) ids.ObjectAddress
	descriptors() iter.Seq2[ids.ObjectAddress, Descriptor]
}

func (a AuthorizationView) definitionDescriptor(address ids.ObjectAddress) (Descriptor, bool) {
	if a.definition != nil {
		return a.definition.descriptor(address)
	}
	return a.catalog.Descriptor(address)
}
func (a AuthorizationView) definitionLookup(key ids.NameKey) ids.ObjectAddress {
	if a.definition != nil {
		return a.definition.lookup(key)
	}
	return a.catalog.NameAddress(key)
}
func (a AuthorizationView) definitionDescriptors() iter.Seq2[ids.ObjectAddress, Descriptor] {
	if a.definition != nil {
		return a.definition.descriptors()
	}
	return a.catalog.Descriptors()
}
func (a AuthorizationView) definitionType(id sqltype.ID) (*sqltype.Descriptor, bool) {
	return definitionTypeAt(a.catalog, a.definition, id)
}

type stageDefinitionReader struct{ stage *CatalogStage }

func (r stageDefinitionReader) descriptor(address ids.ObjectAddress) (Descriptor, bool) {
	if _, removed := r.stage.removals[address]; removed {
		return nil, false
	}
	if descriptor, exists := r.stage.descriptors[address]; exists {
		return descriptor, true
	}
	return r.stage.base.Descriptor(address)
}
func (r stageDefinitionReader) lookup(key ids.NameKey) ids.ObjectAddress {
	return r.stage.lookupName(key)
}
func (r stageDefinitionReader) descriptors() iter.Seq2[ids.ObjectAddress, Descriptor] {
	return func(yield func(ids.ObjectAddress, Descriptor) bool) {
		for address := range r.stage.base.Descriptors() {
			if descriptor, exists := r.descriptor(address); exists {
				if !yield(address, descriptor) {
					return
				}
			}
		}
		for address, descriptor := range r.stage.descriptors {
			if _, original := r.stage.base.Find(address); original {
				continue
			}
			if _, removed := r.stage.removals[address]; removed {
				continue
			}
			if !yield(address, descriptor) {
				return
			}
		}
	}
}

func (a AuthorizationView) definitionNamespace(address ids.ObjectAddress) (*namespaceDescriptor, bool) {
	descriptor, exists := a.definitionDescriptor(address)
	namespace, ok := descriptor.(*namespaceDescriptor)
	return namespace, exists && ok
}

func (b *expressionBinder) definitionReader() definitionReader {
	if b.op != nil && b.op.definition != nil {
		return b.op.definition
	}
	if b.queryAuth != nil {
		return b.queryAuth.definition
	}
	return nil
}
func (b *expressionBinder) definitionDescriptor(address ids.ObjectAddress) (Descriptor, bool) {
	if reader := b.definitionReader(); reader != nil {
		return reader.descriptor(address)
	}
	return b.bindingCatalog().Descriptor(address)
}
func (b *expressionBinder) definitionType(id sqltype.ID) (*sqltype.Descriptor, bool) {
	return definitionTypeAt(b.bindingCatalog(), b.definitionReader(), id)
}
func (b *expressionBinder) definitionArray(element *sqltype.Descriptor) (*sqltype.Descriptor, bool) {
	return definitionArrayAt(b.bindingCatalog(), b.definitionReader(), element)
}

func (a AuthorizationView) definitionRelation(address ids.ObjectAddress) (*relationDescriptor, bool) {
	descriptor, exists := a.definitionDescriptor(address)
	relation, ok := descriptor.(*relationDescriptor)
	return relation, exists && ok
}

func (r stageDefinitionReader) indexLayout(address ids.ObjectAddress) (indexLayoutLineage, bool) {
	lineage, ok := r.stage.indexLayouts[address]
	return lineage, ok
}

func definitionDescriptorAt(view CatalogRead, reader definitionReader, address ids.ObjectAddress) (Descriptor, bool) {
	if reader != nil {
		return reader.descriptor(address)
	}
	return view.Descriptor(address)
}

func (a AuthorizationView) definitionArray(element *sqltype.Descriptor) (*sqltype.Descriptor, bool) {
	return definitionArrayAt(a.catalog, a.definition, element)
}

func definitionTypeAt(view CatalogRead, reader definitionReader, id sqltype.ID) (*sqltype.Descriptor, bool) {
	if reader == nil {
		return view.LookupType(id)
	}
	descriptor, exists := reader.descriptor(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(id)})
	typ, ok := descriptor.(*typeDescriptor)
	if !exists || !ok {
		return nil, false
	}
	return typ.typ, true
}

func definitionArrayAt(view CatalogRead, reader definitionReader, element *sqltype.Descriptor) (*sqltype.Descriptor, bool) {
	if element == nil {
		return nil, false
	}
	if reader == nil {
		return view.LookupArray(element)
	}
	descriptor, exists := reader.descriptor(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(element.ID())})
	typ, ok := descriptor.(*typeDescriptor)
	if !exists || !ok || typ.typ != element {
		return nil, false
	}
	array, ok := definitionTypeAt(view, reader, typ.array)
	return array, ok && array.IsArray() && array.Element() == element
}

func (r scalarRenderer) descriptor(view CatalogRead, address ids.ObjectAddress) Descriptor {
	descriptor, _ := definitionDescriptorAt(view, r.definition, address)
	return descriptor
}

func definitionLookupAt(view CatalogRead, reader definitionReader, key ids.NameKey) ids.ObjectAddress {
	if reader != nil {
		return reader.lookup(key)
	}
	return view.NameAddress(key)
}
