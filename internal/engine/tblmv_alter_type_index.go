package engine

import (
	"stuntgres/internal/engine/catalog"
)

type alterTypeIndex struct {
	index     *catalog.Relation
	keys      []int
	predicate bool
	sql       string
}
