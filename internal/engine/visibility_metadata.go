package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// A namespace's usable exact encoding beats its fallback. Callers visit
// namespaces in path order, so an earlier fallback beats a later exact entry.
// This helper also covers synthetic multi-encoding candidates.
func selectCollation(candidates []collationCandidate, encoding int32) ids.ObjectAddress {
	var fallback ids.ObjectAddress
	for _, candidate := range candidates {
		if candidate.encoding == encoding {
			return candidate.address
		}
		if candidate.encoding == -1 {
			fallback = candidate.address
		}
	}
	return fallback
}

type collationCandidate struct {
	address  ids.ObjectAddress
	encoding int32
}

// Native fixed names are unique. Keep the encoding-bearing lookup inline,
// avoiding an index copy, catalog scan or allocation during row evaluation.
func visibleCollation(view catalog.Read, path []ids.ObjectAddress, d catalog.Descriptor) bool {
	for _, namespace := range path {
		a := view.NameAddress(ids.NameKey{ClassID: ids.CollationClassID, Namespace: namespace, Name: d.Name()})
		candidate, ok := view.Collation(a)
		if !ok {
			continue
		}
		one := [1]collationCandidate{{a, candidate.Encoding}}
		if selected := selectCollation(one[:], 6); selected != (ids.ObjectAddress{}) {
			return selected == d.Address()
		}
	}
	return false
}
