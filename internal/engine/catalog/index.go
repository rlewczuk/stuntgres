package catalog

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// Definitions use stable parent attributes; roots own current live identities.
// Hash equality uses captured immutable buckets; all other reads retain scans.
type IndexKey struct {
	Attribute        int32
	Desc, NullsFirst bool
	Derived          *DerivedIndexKey
	Collation        uint32
	CollationRef     sqltype.CollationRef
	Class            sqltype.IndexClass
	ExplicitClass    bool
}

type IndexDefinition struct {
	Method             IndexMethod
	Parent             ids.ObjectAddress
	PartitionParent    ids.ObjectAddress
	Keys               []IndexKey
	Include            []int32
	Unique             bool
	NullsNotDistinct   bool
	Constraint         *BackingConstraint
	Ready, Valid, Live bool
	Predicate          *IndexPredicate
	Exclusion          []sqltype.ExclusionOperator
}

func (d *IndexDefinition) PublicationState() (ready, valid, live bool) {
	return d.Ready, d.Valid, d.Live
}
