package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
)

// StageBase is the catalog the stage started from.
func (s *CatalogStage) StageBase() catalog.Read { return s.Base }

// recordIndexCandidate records a produced root candidate in the indexes slot.
func (s *CatalogStage) recordIndexCandidate(idx ids.ObjectAddress, candidate index.RootCandidate) {
	if s.indexesOrZero().Candidates == nil {
		s.indexes().Candidates = make(map[ids.ObjectAddress]index.RootCandidate)
	}
	s.indexes().Candidates[idx] = candidate
}

// Removed reports whether the stage removed address.
func (s *CatalogStage) Removed(address ids.ObjectAddress) bool {
	_, removed := s.Removal(address)
	return removed
}

// Replacement returns the descriptor the stage holds for address, or nil.
func (s *CatalogStage) Replacement(address ids.ObjectAddress) catalog.Descriptor {
	return s.Get(address)
}

// indexes returns the indexes slot, allocating it on first use.
func (s *CatalogStage) indexes() *index.Stage {
	if s.indexesSlot == nil {
		s.indexesSlot = new(index.Stage)
	}
	return s.indexesSlot
}

// indexesOrZero reads the indexes slot; a nil slot reads as zero.
func (s *CatalogStage) indexesOrZero() index.Stage {
	if s.indexesSlot == nil {
		return index.Stage{}
	}
	return *s.indexesSlot
}
