package core

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

// retireAnalysisTypeCastsForRebind keeps eligible nominal installed-scalar occurrences
// only in this attempt's candidate. Failure cannot revive the retired original.
func (d *StatementDescription) retireAnalysisTypeCastsForRebind(installedScalarAuthorityRebind bool) map[analyze.PreparedTypeKey]analyze.TypeOccurrence {
	var captures map[analyze.PreparedTypeKey]analyze.TypeOccurrence
	if installedScalarAuthorityRebind {
		for key, captured := range d.types {
			if key.Kind == analyze.IdentityCast && captured.Kind == analyze.IdentityCast && captured.Base.Type != nil && (captured.Base.Type.IsHstore() || (captured.Base.Type.IsLquery() || captured.Base.Type.IsLtree()) || captured.Base.Type.IsEan13() || captured.Base.Type.IsIsbn() || captured.Base.Type.IsIsbn13() || captured.Base.Type.IsIsmn() || captured.Base.Type.IsIsmn13() || captured.Base.Type.IsIssn() || captured.Base.Type.IsIssn13() || captured.Base.Type.IsUpc()) {
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
