package typedef

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// TypedStage is the typed-table observation of one catalog stage: the
// composite type an ALTER TYPE changes and the typed tables it admitted. A
// nil slot observes nothing.
type TypedStage struct {
	Source   ids.ObjectAddress
	Observed map[ids.ObjectAddress]catalog.Descriptor
}

func (t *TypedStage) SourceType() ids.ObjectAddress {
	if t == nil {
		return ids.ObjectAddress{}
	}
	return t.Source
}

func (t *TypedStage) ObservedTables() map[ids.ObjectAddress]catalog.Descriptor {
	if t == nil {
		return nil
	}
	return t.Observed
}
