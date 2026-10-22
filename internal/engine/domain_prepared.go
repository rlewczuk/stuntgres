package engine

import "stuntgres/internal/engine/analyze"

// Environment/definition invalidation permanently retires only domain cast
// occurrences. Frozen composite occurrence identity keeps its existing contract.
// Never mutate a map shared with another description or an admitted binder.
func (d *StatementDescription) retireDomainCasts() {
	for key := range d.types {
		if key.Kind != analyze.DomainCast {
			continue
		}
		next := make(map[analyze.PreparedTypeKey]analyze.TypeOccurrence, len(d.types)-1)
		for k, v := range d.types {
			if k.Kind != analyze.DomainCast {
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

func (d *StatementDescription) publishDomainCasts(captures map[analyze.PreparedTypeKey]analyze.TypeOccurrence) {
	for key := range captures {
		if key.Kind != analyze.DomainCast {
			continue
		}
		next := make(map[analyze.PreparedTypeKey]analyze.TypeOccurrence, len(d.types)+len(captures))
		for k, v := range d.types {
			if k.Kind != analyze.DomainCast {
				next[k] = v
			}
		}
		for k, v := range captures {
			if k.Kind == analyze.DomainCast {
				next[k] = v
			}
		}
		d.types = next
		return
	}
}
