package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// typedStage is the typed-table observation of one catalog stage: the
// composite type an ALTER TYPE changes and the typed tables it admitted. A
// nil slot observes nothing.
type typedStage struct {
	source   ids.ObjectAddress
	observed map[ids.ObjectAddress]catalog.Descriptor
}

// typedState returns the stage's typed-table slot, allocating it on first use.
func (s *CatalogStage) typedState() *typedStage {
	if s.typed == nil {
		s.typed = new(typedStage)
	}
	return s.typed
}

func (t *typedStage) sourceType() ids.ObjectAddress {
	if t == nil {
		return ids.ObjectAddress{}
	}
	return t.source
}

func (t *typedStage) observedTables() map[ids.ObjectAddress]catalog.Descriptor {
	if t == nil {
		return nil
	}
	return t.observed
}
