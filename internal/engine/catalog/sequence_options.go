package catalog

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

type RelationKind uint8

const (
	TableRelation RelationKind = iota
	SequenceRelation
	ViewRelation
	CatalogRelation
	IndexRelation
	CompositeRelation
	MaterializedViewRelation
	PartitionedRelation
	ForeignRelation
)

func (k RelationKind) Stored() bool {
	return k == TableRelation || k == MaterializedViewRelation
}

type SequenceOptions struct {
	SQLType                           sqltype.ID
	Increment, Min, Max, Start, Cache int64
	Cycle                             bool
	Persistence                       string
	Owned                             ids.ObjectAddress
	Identity                          bool
}
