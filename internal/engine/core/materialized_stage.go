package core

import (
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/view"
)

// MaterializedOrZero reads the materialized slot; a nil slot reads as zero.
func (s *CatalogStage) MaterializedOrZero() view.MaterializedStage {
	if s.MaterializedSlot == nil {
		return view.MaterializedStage{}
	}
	return *s.MaterializedSlot
}

// storagePublication names the stage's private stores for storage and index
// publication: initial tables, heap replacements and the refreshed store.
func (s *CatalogStage) storagePublication() storage.Publication {
	p := storage.Publication{Initial: s.initialTables, Shapes: s.RowShapes}
	if r := s.MaterializedOrZero().Replacement; r != nil {
		p.Refreshed, p.Refresh = r.Original.Address(), r.Storage
	}
	return p
}
