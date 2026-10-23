package engine

import "stuntgres/internal/engine/analyze"

// Environment/definition invalidation permanently retires domain and selected
// geometric typename casts. Frozen composite identity keeps its existing contract.
// Never mutate a map shared with another description or an admitted binder.
func (d *StatementDescription) retireAnalysisTypeCasts() {
	for key := range d.types {
		if key.Kind != analyze.DomainCast && key.Kind != analyze.TypeNameCast {
			continue
		}
		next := make(map[analyze.PreparedTypeKey]analyze.TypeOccurrence, len(d.types)-1)
		for k, v := range d.types {
			if k.Kind != analyze.DomainCast && k.Kind != analyze.TypeNameCast {
				next[k] = v
			}
		}
		d.types = next
		if d.analysis != nil {
			d.analysis.needsReanalysis = true
		}
		return
	}
}

func (d *StatementDescription) publishAnalysisTypeCasts(captures map[analyze.PreparedTypeKey]analyze.TypeOccurrence) {
	for key := range captures {
		if key.Kind != analyze.DomainCast && key.Kind != analyze.TypeNameCast {
			continue
		}
		next := make(map[analyze.PreparedTypeKey]analyze.TypeOccurrence, len(d.types)+len(captures))
		for k, v := range d.types {
			if k.Kind != analyze.DomainCast && k.Kind != analyze.TypeNameCast {
				next[k] = v
			}
		}
		for k, v := range captures {
			if k.Kind == analyze.DomainCast || k.Kind == analyze.TypeNameCast {
				next[k] = v
			}
		}
		d.types = next
		return
	}
}
