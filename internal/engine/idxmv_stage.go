package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
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

// indexDescriptorStage is what staging an index descriptor writes through:
// the checked ddl.Stage, the rename of a descriptor staged earlier in the
// statement, and the catalog the stage started from. *CatalogStage
// implements it. Never declare a type over CatalogStage for it: that would
// promote catalog.Candidate's unchecked Create and Replace.
type indexDescriptorStage interface {
	ddl.Stage
	RenameStagedDescriptor(previousKey ids.NameKey, next catalog.Descriptor, nextKey ids.NameKey)
	StageBase() catalog.Read
}
