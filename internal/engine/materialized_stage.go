package engine

import (
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/view"
)

// materialized returns the materialized slot, allocating it on first use.
func (s *CatalogStage) materialized() *view.MaterializedStage {
	if s.materializedSlot == nil {
		s.materializedSlot = new(view.MaterializedStage)
	}
	return s.materializedSlot
}

// materializedOrZero reads the materialized slot; a nil slot reads as zero.
func (s *CatalogStage) materializedOrZero() view.MaterializedStage {
	if s.materializedSlot == nil {
		return view.MaterializedStage{}
	}
	return *s.materializedSlot
}

// storagePublication names the stage's private stores for storage and index
// publication: initial tables, heap replacements and the refreshed store.
func (s *CatalogStage) storagePublication() storage.Publication {
	p := storage.Publication{Initial: s.initialTables, Shapes: s.rowShapes}
	if r := s.materializedOrZero().Replacement; r != nil {
		p.Refreshed, p.Refresh = r.Original.Address(), r.Storage
	}
	return p
}
