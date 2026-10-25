package engine

import (
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/view"
)

// materializedStage returns the materialized slot, allocating it on first use.
func materializedStage(s *CatalogStage) *view.MaterializedStage {
	if s.MaterializedSlot == nil {
		s.MaterializedSlot = new(view.MaterializedStage)
	}
	return s.MaterializedSlot
}

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
