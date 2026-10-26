package core

import (
	"stuntgres/internal/engine/analyze"
)

// expectedOf is the binder's view of a held description: the inputs it reads
// during re-analysis, copied by value. A nil description is not held.
func expectedOf(d *Description) analyze.Expected {
	if d == nil {
		return analyze.Expected{}
	}
	e := analyze.Expected{
		Held:             true,
		Parameters:       d.Parameters,
		Results:          d.Results,
		CatalogOwner:     d.CatalogOwner,
		CatalogEpoch:     d.CatalogEpoch,
		Temporals:        d.Temporals,
		RefreshTemporals: d.RefreshTemporals,
		PlanOwner:        d.PlanOwner,
		PlanGeneration:   d.PlanGeneration,
		Types:            d.Types,
		Sequences:        d.Sequences,
	}
	if d.Regtypes != nil {
		e.Regtypes = d.Regtypes.Constants
	}
	return e
}
