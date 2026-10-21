package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/sqltype"
)

type partitionAttachScan struct {
	Leaf    *catalog.Relation
	Exclude bool
}

type partitionAttachRowConstraint struct {
	Parent  *catalog.Relation
	Child   catalog.Relation
	Layout  []int
	Scratch []sqltype.Value
}
