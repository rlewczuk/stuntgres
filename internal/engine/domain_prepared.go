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
