package engine

import (
	"stuntgres/internal/engine/catalog"
)

type alterTypeIndex struct {
	Index     *catalog.Relation
	Keys      []int
	Predicate bool
	SQL       string
}
