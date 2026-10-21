package engine

import (
	"iter"

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
	return catalog.DefinitionProceduresNamedAt(a.Catalog, a.definition, namespace, name)
}

func (a AuthorizationView) definitionType(id sqltype.ID) (*sqltype.Descriptor, bool) {
	return catalog.DefinitionTypeAt(a.Catalog, a.definition, id)
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
	return catalog.DefinitionNamespaceAt(a.Catalog, a.definition, address)
}

func (a AuthorizationView) definitionRelation(address ids.ObjectAddress) (*catalog.Relation, bool) {
	return catalog.DefinitionRelationAt(a.Catalog, a.definition, address)
}

func (r stageDefinitionReader) IndexLayout(address ids.ObjectAddress) (indexLayoutLineage, bool) {
	lineage, ok := r.stage.indexesOrZero().Layouts[address]
	return lineage, ok
}

func (a AuthorizationView) definitionArray(element *sqltype.Descriptor) (*sqltype.Descriptor, bool) {
	return catalog.DefinitionArrayAt(a.Catalog, a.definition, element)
}
