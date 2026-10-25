package core

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
)

// validateIndexMaintenance forwards the stage's index observations and the
// original of a replaced materialized view to index.ValidateMaintenance.
func (s *CatalogStage) validateIndexMaintenance(current *catalog.State) error {
	var replacedParent ids.ObjectAddress
	if replacement := s.MaterializedOrZero().Replacement; replacement != nil {
		replacedParent = replacement.Original.Address()
	}
	return index.ValidateMaintenance(s.IndexesOrZero(), s.Base, current, replacedParent)
}

// indexes returns the statement's indexes slot, allocating it on first use.
func (s *StatementScope) indexes() *index.Scope {
	if s.indexesSlot == nil {
		s.indexesSlot = new(index.Scope)
	}
	return s.indexesSlot
}

// IndexesOrZero reads the indexes slot; a nil slot reads as zero.
func (s *StatementScope) IndexesOrZero() index.Scope {
	if s.indexesSlot == nil {
		return index.Scope{}
	}
	return *s.indexesSlot
}
