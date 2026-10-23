package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
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

func (a AuthorizationView) definitionType(id sqltype.ID) (*sqltype.Descriptor, bool) {
	return catalog.DefinitionTypeAt(a.Catalog, a.definition, id)
}

// stageDefinitionReader is the definition reader of a catalog stage: its
// candidate's definitions plus the stage's own index layouts.
type stageDefinitionReader struct {
	candidateDefinitions
	stage *CatalogStage
}

// stageDefinitions returns stage's definition reader. Construction never
// dereferences stage; reading through the reader of a nil stage panics.
func stageDefinitions(stage *CatalogStage) stageDefinitionReader {
	if stage == nil {
		return stageDefinitionReader{}
	}
	return stageDefinitionReader{candidateDefinitions{&stage.Candidate}, stage}
}

func (a AuthorizationView) definitionNamespace(address ids.ObjectAddress) (*catalog.Namespace, bool) {
	return catalog.DefinitionNamespaceAt(a.Catalog, a.definition, address)
}

func (r stageDefinitionReader) IndexLayout(address ids.ObjectAddress) (index.LayoutLineage, bool) {
	lineage, ok := r.stage.indexesOrZero().Layouts[address]
	return lineage, ok
}

func (a AuthorizationView) definitionArray(element *sqltype.Descriptor) (*sqltype.Descriptor, bool) {
	return catalog.DefinitionArrayAt(a.Catalog, a.definition, element)
}
