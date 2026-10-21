package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/parser"
)

type partitionUsingIndexReplay struct {
	Target, Index *catalog.Relation
	Syntax        *parser.AlterTable
}
