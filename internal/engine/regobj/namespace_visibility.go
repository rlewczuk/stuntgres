package regobj

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// Search-path visibility of named catalog objects; MVCC visibility lives in
// the visibility package.

// A namespace's usable exact encoding beats its fallback. Callers visit
// namespaces in path order, so an earlier fallback beats a later exact entry.
// This helper also covers synthetic multi-encoding candidates.
func SelectCollation(candidates []CollationCandidate, encoding int32) ids.ObjectAddress {
	var fallback ids.ObjectAddress
	for _, candidate := range candidates {
		if candidate.Encoding == encoding {
			return candidate.Address
		}
		if candidate.Encoding == -1 {
			fallback = candidate.Address
		}
	}
	return fallback
}

type CollationCandidate struct {
	Address  ids.ObjectAddress
	Encoding int32
}

// Native fixed names are unique. Keep the encoding-bearing lookup inline,
// avoiding an index copy, catalog scan or allocation during row evaluation.
func VisibleCollation(view catalog.Read, path []ids.ObjectAddress, d catalog.Descriptor) bool {
	for _, namespace := range path {
		a := view.NameAddress(ids.NameKey{ClassID: ids.CollationClassID, Namespace: namespace, Name: d.Name()})
		candidate, ok := view.Collation(a)
		if !ok {
			continue
		}
		one := [1]CollationCandidate{{a, candidate.Encoding}}
		if selected := SelectCollation(one[:], 6); selected != (ids.ObjectAddress{}) {
			return selected == d.Address()
		}
	}
	return false
}

func NamedObjectVisible(view catalog.Read, path []ids.ObjectAddress, d catalog.Descriptor) bool {
	return NamedObjectVisibleAt(view, nil, path, d)
}

// NamedObjectVisibleAt uses the same exact-name/signature selection over staged definitions.
func NamedObjectVisibleAt(view catalog.Read, reader catalog.DefinitionReader, path []ids.ObjectAddress, d catalog.Descriptor) bool {
	for _, namespace := range path {
		key := catalog.DescriptorNameKey(d)
		key.Namespace = namespace
		if found := catalog.DefinitionLookupAt(view, reader, key); found != (ids.ObjectAddress{}) {
			return found == d.Address()
		}
	}
	return false
}
