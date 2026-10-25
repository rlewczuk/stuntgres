package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

func inheritanceUnitParents(parents ...ids.ObjectAddress) []catalog.InheritanceParent {
	edges := make([]catalog.InheritanceParent, len(parents))
	for i, parent := range parents {
		edges[i] = catalog.InheritanceParent{Relation: parent, Seqno: int32(i + 1)}
	}
	return edges
}
