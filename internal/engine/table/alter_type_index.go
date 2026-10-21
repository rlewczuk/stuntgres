package table

import (
	"stuntgres/internal/engine/catalog"
)

type AlterTypeIndex struct {
	Index     *catalog.Relation
	Keys      []int
	Predicate bool
	SQL       string
}
