package analyze

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/sequence"
)

// Expected is what a held statement description gives its re-analysis: the
// fixed public signature and the captures that keep native identities (logical
// literals, sequence and catalog-identifier constants) across invalidation.
// The zero value, with Held false, binds a statement for the first time. The
// binder only reads it.
type Expected struct {
	Held       bool
	Parameters ParameterInfo
	Results    []catalog.Column
	// Logical identities belong to a catalog owner and epoch, not merely an OID.
	CatalogOwner *catalog.Owner
	CatalogEpoch ids.CatalogEpoch
	Temporals    []TemporalConstant
	// RefreshTemporals asks a full invalidation to recollect temporal-now
	// captures; it is set only on candidates, never on a published description.
	RefreshTemporals bool
	PlanOwner        ids.SessionToken
	PlanGeneration   uint64
	Composites       map[PreparedCompositeKey]CompositeOccurrence
	Sequences        *sequence.Analysis
	Regtypes         []RegtypeConstant
}
