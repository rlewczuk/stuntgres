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
	ClassOptions     IndexClassOptions
}

// IndexClassOptions retains the sole native per-key option without shared
// mutable storage. Absent options stay absent in catalogs and SQL replay.
type IndexClassOptions struct {
	Siglen   int64
	Text     string
	Explicit bool
}

func (o IndexClassOptions) EffectiveSiglen() int64 {
	if o.Explicit {
		return o.Siglen
	}
	return 124
}

type IndexDefinition struct {
	Method             IndexMethod
	MethodOID          ids.OID
	Parent             ids.ObjectAddress
	PartitionParent    ids.ObjectAddress
	Keys               []IndexKey
	Include            []int32
	Unique             bool
	NullsNotDistinct   bool
	Constraint         *BackingConstraint
	Ready, Valid, Live bool
	Clustered          bool
	Predicate          *IndexPredicate
	Exclusion          []sqltype.ExclusionOperator
}

// MethodIdentity is the declared SQL method, independent of its implementation.
func (d *IndexDefinition) MethodIdentity() ids.OID { return d.MethodOID }

func (d *IndexDefinition) PublicationState() (ready, valid, live bool) {
	return d.Ready, d.Valid, d.Live
}
