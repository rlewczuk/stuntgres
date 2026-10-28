package table

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/stored"
)

// TriggerModesEqual compares only the state a trigger-only ALTER can change.
// Both descriptors come from the same stage and retain structural identities.
func TriggerModesEqual(a, b *catalog.Relation) bool {
	if a == b {
		return true
	}
	users, other := stored.TriggersOf(a), stored.TriggersOf(b)
	if len(users) != len(other) || len(a.Constraints.ForeignKeys) != len(b.Constraints.ForeignKeys) {
		return false
	}
	for i, t := range users {
		if t.OID() != other[i].OID() || t.Mode() != other[i].Mode() {
			return false
		}
	}
	for i, fk := range a.Constraints.ForeignKeys {
		prior := b.Constraints.ForeignKeys[i]
		if fk.OID != prior.OID || fk.TriggerOIDs != prior.TriggerOIDs {
			return false
		}
		for slot := range fk.TriggerOIDs {
			if fk.TriggerMode(slot) != prior.TriggerMode(slot) {
				return false
			}
		}
	}
	return true
}
