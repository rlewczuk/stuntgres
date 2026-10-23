package engine

import "stuntgres/internal/engine/analyze"

// retireAnalysisTypeCasts retires the description's domain and type-name
// casts (analyze.RetireDomainCasts) and makes that invalidation sticky, so a
// failed reanalysis cannot revive them when the path is restored.
func (d *StatementDescription) retireAnalysisTypeCasts() {
	types, retired := analyze.RetireDomainCasts(d.types)
	if !retired {
		return
	}
	d.types = types
	if d.analysis != nil {
		d.analysis.needsReanalysis = true
	}
}

// retireAnalysisTypeCastsForRebind keeps eligible nominal EAN13 occurrences
// only in this attempt's candidate. Failure cannot revive the retired original.
func (d *StatementDescription) retireAnalysisTypeCastsForRebind(eanAuthorityRebind bool) map[analyze.PreparedTypeKey]analyze.TypeOccurrence {
	var captures map[analyze.PreparedTypeKey]analyze.TypeOccurrence
	if eanAuthorityRebind {
		for key, captured := range d.types {
			if key.Kind == analyze.IdentityCast && captured.Kind == analyze.IdentityCast && captured.Base.Type != nil && captured.Base.Type.IsEan13() {
				if captures == nil {
					captures = make(map[analyze.PreparedTypeKey]analyze.TypeOccurrence)
				}
				captures[key] = captured
			}
		}
	}
	d.retireAnalysisTypeCasts()
	return analyze.PublishDomainCasts(d.types, captures)
}
