package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/sqltype"
)

func (a AuthorizationView) definitionDescriptor(address ids.ObjectAddress) (catalog.Descriptor, bool) {
	if a.Definition != nil {
		return a.Definition.Descriptor(address)
	}
	return a.Catalog.Descriptor(address)
}
func (a AuthorizationView) DefinitionLookup(key ids.NameKey) ids.ObjectAddress {
	if a.Definition != nil {
		return a.Definition.Lookup(key)
	}
	return a.Catalog.NameAddress(key)
}

func (a AuthorizationView) definitionType(id sqltype.ID) (*sqltype.Descriptor, bool) {
	return catalog.DefinitionTypeAt(a.Catalog, a.Definition, id)
}

// stageDefinitionReader is the definition reader of a catalog stage: its
// candidate's definitions plus the stage's own index layouts.
type stageDefinitionReader struct {
	catalog.CandidateDefinitions
	stage *CatalogStage
}

// StageDefinitions returns stage's definition reader. Construction never
// dereferences stage; reading through the reader of a nil stage panics.
func StageDefinitions(stage *CatalogStage) stageDefinitionReader {
	if stage == nil {
		return stageDefinitionReader{}
	}
	return stageDefinitionReader{stage.Definitions(), stage}
}

func (r stageDefinitionReader) HasProviderProcedureOverride(name string) bool {
	if r.stage.Base.State().HasProviderProcedureOverride(name) {
		return true
	}
	for address, descriptor := range r.stage.Descriptors() {
		current, ok := descriptor.(*catalog.Procedure)
		original, existed := r.stage.Base.Procedure(address)
		if !ok || !existed || original.Builtin == nil && original.Implementation == nil {
			continue
		}
		if (original.Name() != current.Name() || original.Namespace() != current.Namespace()) && (original.Name() == name || current.Name() == name) {
			return true
		}
	}
	return false
}

func (r stageDefinitionReader) IndexLayout(address ids.ObjectAddress) (index.LayoutLineage, bool) {
	lineage, ok := r.stage.IndexesOrZero().Layouts[address]
	return lineage, ok
}

func (a AuthorizationView) definitionArray(element *sqltype.Descriptor) (*sqltype.Descriptor, bool) {
	return catalog.DefinitionArrayAt(a.Catalog, a.Definition, element)
}
