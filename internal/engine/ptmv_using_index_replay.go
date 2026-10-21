package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/parser"
)

type partitionUsingIndexReplay struct {
	target, index *catalog.Relation
	syntax        *parser.AlterTable
}
