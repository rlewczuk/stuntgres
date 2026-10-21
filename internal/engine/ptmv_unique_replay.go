package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/parser"
)

type partitionUniqueReplay struct {
	target      *catalog.Relation
	syntax      *parser.AlterTable
	definition  *parser.UniqueConstraint
	snapshot    parser.UniqueConstraint
	descriptors []*catalog.Relation
}
