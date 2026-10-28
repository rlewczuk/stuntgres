package catalog

import "stuntgres/internal/engine/ids"

// DroppedObject is a captured pre-deletion identity. Names and Args are immutable
// after capture; presence flags distinguish SQL NULL from an empty identifier.
// Found distinguishes an absent address from a captured object with empty fields.
type DroppedObject struct {
	Address                      ids.ObjectAddress
	Found                        bool
	Original, Normal, Temporary  bool
	HasSchema, HasName           bool
	Schema, Name, Kind, Identity string
	Names, Args                  []string
}

// DropProvenance retains independent incoming deletion-path flags.
type DropProvenance struct{ Original, Normal bool }

// CollectDroppedObjects is the allocation-free session-independent superset:
// origin, replica and always may fire; command scope applies the effective role.
func CollectDroppedObjects(view Read) bool {
	if view.state == nil || view.state.eventTriggers.Len() == 0 {
		return false
	}
	return collectIndexedDroppedObjects(view)
}

// Keep iterator callback escape state off the absent-index path.
func collectIndexedDroppedObjects(view Read) bool {
	for _, trigger := range view.EventTriggers() {
		if trigger.FiringMode.Valid() && trigger.FiringMode != TriggerDisabled && trigger.Event == "sql_drop" {
			return true
		}
	}
	return false
}
