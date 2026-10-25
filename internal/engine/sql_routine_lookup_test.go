package engine

import (
	"iter"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
)

// sqlRoutineLookupSource is the staged definition reader's method set.
type sqlRoutineLookupSource interface {
	catalog.DefinitionReader
	IndexLayout(ids.ObjectAddress) (index.LayoutLineage, bool)
}

// Instrument the real staged reader rather than replacing its descriptors or
// provider behavior. Callable lookup must not invoke its full discovery API.
type sqlRoutineLookupReader struct {
	sqlRoutineLookupSource
	fullEnumerations int
}

func (r *sqlRoutineLookupReader) Descriptors() iter.Seq2[ids.ObjectAddress, catalog.Descriptor] {
	r.fullEnumerations++
	return r.sqlRoutineLookupSource.Descriptors()
}
