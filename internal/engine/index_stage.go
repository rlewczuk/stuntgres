package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// StageBase is the catalog the stage started from.
func (s *CatalogStage) StageBase() catalog.Read { return s.Base }

// recordIndexCandidate records a produced root candidate in the indexes slot.
func (s *CatalogStage) recordIndexCandidate(index ids.ObjectAddress, candidate indexRootCandidate) {
	if s.indexesOrZero().candidates == nil {
		s.indexes().candidates = make(map[ids.ObjectAddress]indexRootCandidate)
	}
	s.indexes().candidates[index] = candidate
}

// removed reports whether the stage removed address.
func (s *CatalogStage) removed(address ids.ObjectAddress) bool {
	_, removed := s.Removal(address)
	return removed
}

// replacement returns the descriptor the stage holds for address, or nil.
func (s *CatalogStage) replacement(address ids.ObjectAddress) catalog.Descriptor {
	return s.Get(address)
}

// indexes returns the indexes slot, allocating it on first use.
func (s *CatalogStage) indexes() *indexStage {
	if s.indexesSlot == nil {
		s.indexesSlot = new(indexStage)
	}
	return s.indexesSlot
}

// indexesOrZero reads the indexes slot; a nil slot reads as zero.
func (s *CatalogStage) indexesOrZero() indexStage {
	if s.indexesSlot == nil {
		return indexStage{}
	}
	return *s.indexesSlot
}
