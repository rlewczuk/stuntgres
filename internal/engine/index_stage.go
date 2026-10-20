package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// indexStage is a catalog stage's index work: REINDEX intents, produced
// candidates, transported layouts, parent and index-set observations, and
// the output environment for index expressions. Index DDL and the features
// that build indexes write it.
type indexStage struct {
	rebuilds   map[ids.ObjectAddress]*catalog.Relation
	candidates map[ids.ObjectAddress]indexRootCandidate
	layouts    map[ids.ObjectAddress]indexLayoutLineage
	parents    map[ids.ObjectAddress]*catalog.Relation
	sets       map[ids.ObjectAddress]int
	output     sqltype.OutputEnvironment
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
