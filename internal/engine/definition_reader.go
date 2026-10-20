package engine

import (
	"iter"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func (a AuthorizationView) definitionDescriptor(address ids.ObjectAddress) (catalog.Descriptor, bool) {
	if a.definition != nil {
		return a.definition.Descriptor(address)
	}
	return a.Catalog.Descriptor(address)
}
func (a AuthorizationView) definitionLookup(key ids.NameKey) ids.ObjectAddress {
	if a.definition != nil {
		return a.definition.Lookup(key)
	}
	return a.Catalog.NameAddress(key)
}
func (a AuthorizationView) definitionDescriptors() iter.Seq2[ids.ObjectAddress, catalog.Descriptor] {
	if a.definition != nil {
		return a.definition.Descriptors()
	}
	return a.Catalog.Descriptors()
}

func (a AuthorizationView) definitionProceduresNamed(namespace ids.ObjectAddress, name string) iter.Seq[*catalog.Procedure] {
	return definitionProceduresNamedAt(a.Catalog, a.definition, namespace, name)
}

func definitionProceduresNamedAt(view catalog.Read, reader catalog.DefinitionReader, namespace ids.ObjectAddress, name string) iter.Seq[*catalog.Procedure] {
	if reader != nil {
		return reader.ProceduresNamed(namespace, name)
	}
	return indexedDefinitionProcedures(view, nil, namespace, name)
}

// Enumeration discovers candidates; the selected provider still supplies the
// ordinary exact dependency and permission proof.
func indexedDefinitionProcedures(view catalog.Read, reader catalog.DefinitionReader, namespace ids.ObjectAddress, name string) iter.Seq[*catalog.Procedure] {
	return func(yield func(*catalog.Procedure) bool) {
		for _, address := range view.State().ProceduresNamed(namespace, name) {
			descriptor, present := definitionDescriptorAt(view, reader, address)
			if d, ok := descriptor.(*catalog.Procedure); present && ok && d.Namespace() == namespace && d.Name() == name {
				if !yield(d) {
					return
				}
			}
		}
	}
}
func (a AuthorizationView) definitionType(id sqltype.ID) (*sqltype.Descriptor, bool) {
	return definitionTypeAt(a.Catalog, a.definition, id)
}

type stageDefinitionReader struct{ stage *CatalogStage }

func (r stageDefinitionReader) Descriptor(address ids.ObjectAddress) (catalog.Descriptor, bool) {
	if _, removed := r.stage.Removal(address); removed {
		return nil, false
	}
	if descriptor, exists := r.stage.Find(address); exists {
		return descriptor, true
	}
	return r.stage.Base.Descriptor(address)
}
func (r stageDefinitionReader) Lookup(key ids.NameKey) ids.ObjectAddress {
	return r.stage.LookupName(key)
}
func (r stageDefinitionReader) Descriptors() iter.Seq2[ids.ObjectAddress, catalog.Descriptor] {
	return func(yield func(ids.ObjectAddress, catalog.Descriptor) bool) {
		for address := range r.stage.Base.Descriptors() {
			if descriptor, exists := r.Descriptor(address); exists {
				if !yield(address, descriptor) {
					return
				}
			}
		}
		for address, descriptor := range r.stage.Descriptors() {
			if _, original := r.stage.Base.Find(address); original {
				continue
			}
			if _, removed := r.stage.Removal(address); removed {
				continue
			}
			if !yield(address, descriptor) {
				return
			}
		}
	}
}

func (r stageDefinitionReader) ProceduresNamed(namespace ids.ObjectAddress, name string) iter.Seq[*catalog.Procedure] {
	return func(yield func(*catalog.Procedure) bool) {
		for _, address := range r.stage.Base.State().ProceduresNamed(namespace, name) {
			if _, overridden := r.stage.Find(address); overridden {
				continue
			}
			descriptor, present := r.Descriptor(address)
			if d, ok := descriptor.(*catalog.Procedure); present && ok && d.Namespace() == namespace && d.Name() == name {
				if !yield(d) {
					return
				}
			}
		}
		for address, descriptor := range r.stage.Descriptors() {
			if _, removed := r.stage.Removal(address); removed {
				continue
			}
			if d, ok := descriptor.(*catalog.Procedure); ok && d.Namespace() == namespace && d.Name() == name {
				if !yield(d) {
					return
				}
			}
		}
	}
}

func (r stageDefinitionReader) HasSQLProcedureName(name string) bool {
	count := 0
	if index := r.stage.Base.State().ProcedureNames(); index != nil {
		count = index.SQLNames[name]
	}
	if count == 0 {
		for address, descriptor := range r.stage.Descriptors() {
			if d, ok := descriptor.(*catalog.Procedure); ok && d.SQL != nil && d.Name() == name {
				if _, removed := r.stage.Removal(address); !removed {
					return true
				}
			}
		}
		return false
	}
	for address := range r.stage.Removals() {
		if d, ok := r.stage.Base.Procedure(address); ok && d.SQL != nil && d.Name() == name {
			count--
		}
	}
	for address, descriptor := range r.stage.Descriptors() {
		if _, removed := r.stage.Removal(address); removed {
			continue
		}
		if old, ok := r.stage.Base.Procedure(address); ok && old.SQL != nil && old.Name() == name {
			count--
		}
		if d, ok := descriptor.(*catalog.Procedure); ok && d.SQL != nil && d.Name() == name {
			count++
		}
	}
	return count > 0
}

func (a AuthorizationView) definitionNamespace(address ids.ObjectAddress) (*catalog.Namespace, bool) {
	return definitionNamespaceAt(a.Catalog, a.definition, address)
}

func definitionNamespaceAt(view catalog.Read, reader catalog.DefinitionReader, address ids.ObjectAddress) (*catalog.Namespace, bool) {
	descriptor, exists := definitionDescriptorAt(view, reader, address)
	namespace, ok := descriptor.(*catalog.Namespace)
	return namespace, exists && ok
}

func (b *expressionBinder) definitionReader() catalog.DefinitionReader {
	return b.host.BindingDefinition(b.query.auth)
}

func (b *expressionBinder) hasSQLProcedureName(name string) bool {
	if reader := b.definitionReader(); reader != nil {
		return reader.HasSQLProcedureName(name)
	}
	return b.bindingCatalog().State().HasSQLProcedureName(name)
}
func (b *expressionBinder) definitionDescriptor(address ids.ObjectAddress) (catalog.Descriptor, bool) {
	if reader := b.definitionReader(); reader != nil {
		return reader.Descriptor(address)
	}
	return b.bindingCatalog().Descriptor(address)
}
func (b *expressionBinder) definitionType(id sqltype.ID) (*sqltype.Descriptor, bool) {
	return definitionTypeAt(b.bindingCatalog(), b.definitionReader(), id)
}
func (b *expressionBinder) definitionArray(element *sqltype.Descriptor) (*sqltype.Descriptor, bool) {
	return definitionArrayAt(b.bindingCatalog(), b.definitionReader(), element)
}

// The authority* reads go through auth's own catalog and definition reader,
// which the host supplies (a binding authority reads through the operation's).
func (b *expressionBinder) authorityDescriptor(auth analyze.Authority, address ids.ObjectAddress) (catalog.Descriptor, bool) {
	return definitionDescriptorAt(auth.ReadCatalog(), b.host.DefinitionOf(auth), address)
}
func (b *expressionBinder) authorityRelation(auth analyze.Authority, address ids.ObjectAddress) (*catalog.Relation, bool) {
	return definitionRelationAt(auth.ReadCatalog(), b.host.DefinitionOf(auth), address)
}
func (b *expressionBinder) authorityNamespace(auth analyze.Authority, address ids.ObjectAddress) (*catalog.Namespace, bool) {
	return definitionNamespaceAt(auth.ReadCatalog(), b.host.DefinitionOf(auth), address)
}
func (b *expressionBinder) authorityType(auth analyze.Authority, id sqltype.ID) (*sqltype.Descriptor, bool) {
	return definitionTypeAt(auth.ReadCatalog(), b.host.DefinitionOf(auth), id)
}

func (a AuthorizationView) definitionRelation(address ids.ObjectAddress) (*catalog.Relation, bool) {
	return definitionRelationAt(a.Catalog, a.definition, address)
}

func definitionRelationAt(view catalog.Read, reader catalog.DefinitionReader, address ids.ObjectAddress) (*catalog.Relation, bool) {
	descriptor, exists := definitionDescriptorAt(view, reader, address)
	relation, ok := descriptor.(*catalog.Relation)
	return relation, exists && ok
}

func (r stageDefinitionReader) indexLayout(address ids.ObjectAddress) (indexLayoutLineage, bool) {
	lineage, ok := r.stage.indexesOrZero().layouts[address]
	return lineage, ok
}

func definitionDescriptorAt(view catalog.Read, reader catalog.DefinitionReader, address ids.ObjectAddress) (catalog.Descriptor, bool) {
	if reader != nil {
		return reader.Descriptor(address)
	}
	return view.Descriptor(address)
}

func (a AuthorizationView) definitionArray(element *sqltype.Descriptor) (*sqltype.Descriptor, bool) {
	return definitionArrayAt(a.Catalog, a.definition, element)
}

func definitionTypeAt(view catalog.Read, reader catalog.DefinitionReader, id sqltype.ID) (*sqltype.Descriptor, bool) {
	if reader == nil {
		return view.LookupType(id)
	}
	descriptor, exists := reader.Descriptor(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(id)})
	typ, ok := descriptor.(*catalog.Type)
	if !exists || !ok {
		return nil, false
	}
	return typ.SQLType, true
}

func definitionArrayAt(view catalog.Read, reader catalog.DefinitionReader, element *sqltype.Descriptor) (*sqltype.Descriptor, bool) {
	if element == nil {
		return nil, false
	}
	if reader == nil {
		return view.LookupArray(element)
	}
	descriptor, exists := reader.Descriptor(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(element.ID())})
	typ, ok := descriptor.(*catalog.Type)
	if !exists || !ok || typ.SQLType != element {
		return nil, false
	}
	array, ok := definitionTypeAt(view, reader, typ.Array)
	return array, ok && array.IsArray() && array.Element() == element
}

func (r scalarRenderer) descriptor(view catalog.Read, address ids.ObjectAddress) catalog.Descriptor {
	descriptor, _ := definitionDescriptorAt(view, r.definition, address)
	return descriptor
}

func definitionLookupAt(view catalog.Read, reader catalog.DefinitionReader, key ids.NameKey) ids.ObjectAddress {
	if reader != nil {
		return reader.Lookup(key)
	}
	return view.NameAddress(key)
}
