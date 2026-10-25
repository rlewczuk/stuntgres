package core

import (
	"stuntgres/internal/engine/analyze"
)

// expectedOf is the binder's view of a held description: the inputs it reads
// during re-analysis, copied by value. A nil description is not held.
func expectedOf(d *StatementDescription) analyze.Expected {
	if d == nil {
		return analyze.Expected{}
	}
	e := analyze.Expected{
		Held:             true,
		Parameters:       d.Parameters,
		Results:          d.Results,
		CatalogOwner:     d.catalogOwner,
		CatalogEpoch:     d.catalogEpoch,
		Temporals:        d.Temporals,
		RefreshTemporals: d.refreshTemporals,
		PlanOwner:        d.planOwner,
		PlanGeneration:   d.planGeneration,
		Types:            d.types,
		Sequences:        d.Sequences,
	}
	if d.regtypes != nil {
		e.Regtypes = d.regtypes.Constants
	}
	return e
}
