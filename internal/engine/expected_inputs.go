package engine

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
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
		Temporals:        d.temporals,
		RefreshTemporals: d.refreshTemporals,
		PlanOwner:        d.planOwner,
		PlanGeneration:   d.planGeneration,
		Composites:       d.composites,
		Sequences:        d.sequences,
	}
	if d.regtypes != nil {
		e.Regtypes = d.regtypes.constants
	}
	return e
}

// checkExpectedLifetime applies the held signature check to a catalog owner
// or epoch replacement of the held description.
func checkExpectedLifetime(e analyze.Expected, owner *catalog.Owner, epoch ids.CatalogEpoch) error {
	expected := statementSignature{parameters: e.Parameters, results: e.Results, owner: e.CatalogOwner, epoch: e.CatalogEpoch}
	actual := expected
	actual.owner, actual.epoch = owner, epoch
	return checkSignature(expected, actual)
}
