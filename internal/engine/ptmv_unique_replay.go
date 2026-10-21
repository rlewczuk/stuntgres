package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/parser"
)

type partitionUniqueReplay struct {
	Target      *catalog.Relation
	Syntax      *parser.AlterTable
	Definition  *parser.UniqueConstraint
	Snapshot    parser.UniqueConstraint
	Descriptors []*catalog.Relation
}
