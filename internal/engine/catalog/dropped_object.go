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

// CollectDroppedObjects is the indexed, allocation-free collection admission.
func CollectDroppedObjects(view Read) bool {
	if view.State() == nil {
		return false
	}
	for _, trigger := range view.EventTriggers() {
		if trigger.Enabled && trigger.Event == "sql_drop" {
			return true
		}
	}
	return false
}
