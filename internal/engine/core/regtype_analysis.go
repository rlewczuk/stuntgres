package core

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/sequence"
)

// RegtypeObservationLocked is what prepared reg* literals are analyzed and
// validated against, with auth's type and collation lookup paths. Caller
// holds Engine.mu; the path callbacks run under it.
func (e *Engine) RegtypeObservationLocked(auth AuthorizationView) (regobj.Observation, regobj.LookupPaths) {
	return regobj.Observation{Domain: e.Catalog.Domain, Database: auth.Session.DatabaseName, View: auth.View, Environment: auth},
		regobj.LookupPaths{
			Type:      func() (sequence.Environment, error) { return e.sequenceEnvironmentLocked(auth) },
			Collation: func() ([]ids.ObjectAddress, error) { return LookupPath(auth, analyze.CollationLookup) },
		}
}

// regtypeAnalysisLocked captures a prepared statement's reg* literals. A
// statement without them builds no observation.
func (e *Engine) regtypeAnalysisLocked(auth AuthorizationView, constants []analyze.RegtypeConstant) (*regobj.Analysis, error) {
	if len(constants) == 0 {
		return nil, nil
	}
	o, paths := e.RegtypeObservationLocked(auth)
	return regobj.NewAnalysis(o, paths, constants)
}

// regtypeAnalysisValidLocked validates a held cell; a missing cell is valid
// without an observation.
func (e *Engine) regtypeAnalysisValidLocked(auth AuthorizationView, cell *regobj.Analysis) (bool, error) {
	if cell == nil {
		return true, nil
	}
	o, paths := e.RegtypeObservationLocked(auth)
	return cell.Valid(o, paths)
}

// regtypeReanalysisLocked preserves only eligible original configuration inputs
// in an attempt-private projection. Caller holds Engine.Mu.
func (e *Engine) regtypeReanalysisLocked(auth AuthorizationView, expected *Description) (*regobj.Analysis, error) {
	cell := expected.Regtypes
	if cell == nil {
		return nil, nil
	}
	analysis := expected.Analysis
	if analysis == nil || expected.CatalogOwner != auth.Catalog.Owner() ||
		expected.CatalogEpoch != auth.Catalog.Epoch() || expected.PlanOwner != auth.Session.SessionToken ||
		expected.PlanGeneration != auth.Session.DiscardPlansGeneration ||
		analysis.Domain != e.Catalog.Domain || analysis.Epoch != auth.Catalog.Epoch() ||
		analysis.Session != auth.Session.SessionToken || analysis.Private != auth.PendingCatalog() {
		cell.RetireConfigurations()
		return nil, nil
	}
	o, paths := e.RegtypeObservationLocked(auth)
	return cell.ForReanalysis(o, paths)
}
