package catalog

import (
	"slices"

	"stuntgres/internal/engine/ids"
)

func DefaultRecordsUnchanged(current *State, reads map[DefaultACLKey]*DefaultACLRecord) bool {
	for key, record := range reads {
		if current.defaultACLs[key] != record {
			return false
		}
	}
	return true
}

// Projection removes namespace-owned directives with their namespaces. Physical
// retirement removes their directives while retaining empty namespace metadata.
// Invalid already-orphaned keys remain for validation.
func PruneNamespaceDefaults(source, next *State, retired []ids.ObjectAddress) {
	copied := false
	for key := range next.defaultACLs {
		if key.Schema == (ids.ObjectAddress{}) || source.Get(key.Schema) == nil || next.Get(key.Schema) != nil && !slices.Contains(retired, key.Schema) {
			continue
		}
		if !copied {
			next.defaultACLs = CloneDefaultACLs(next.defaultACLs)
			copied = true
		}
		delete(next.defaultACLs, key)
	}
}

func RemapDefaultSchemas(state *State, mapping map[ids.ObjectAddress]ids.ObjectAddress) {
	original := state.defaultACLs
	copied := false
	for key, record := range original {
		destination, changed := mapping[key.Schema]
		if !changed {
			continue
		}
		if !copied {
			state.defaultACLs = CloneDefaultACLs(original)
			copied = true
		}
		delete(state.defaultACLs, key)
		key.Schema = destination
		state.defaultACLs[key] = record
	}
}
