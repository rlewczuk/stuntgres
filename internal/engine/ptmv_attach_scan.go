package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/sqltype"
)

type partitionAttachScan struct {
	leaf    *catalog.Relation
	exclude bool
}

type partitionAttachRowConstraint struct {
	parent  *catalog.Relation
	child   catalog.Relation
	layout  []int
	scratch []sqltype.Value
}
