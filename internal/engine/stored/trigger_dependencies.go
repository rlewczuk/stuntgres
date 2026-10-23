package stored

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

func TriggerDepends(t Trigger, target ids.ObjectAddress) bool {
	for _, reference := range t.parts.References {
		if reference == target || target.SubID == 0 && reference.ClassID == target.ClassID && reference.ObjectID == target.ObjectID {
			return true
		}
	}
	return false
}

func KeepTriggers(list []Trigger, targets []ids.ObjectAddress) []Trigger {
	var kept []Trigger
	for _, t := range list {
		remove := false
		for _, target := range targets {
			if TriggerDepends(t, target) {
				remove = true
				break
			}
		}
		if !remove {
			kept = append(kept, t)
		}
	}
	return kept
}

func ValidateTriggers(read catalog.Read, d *catalog.Relation) error {
	if d.Triggers == nil {
		return nil
	}
	list, ok := d.Triggers.(TriggerList)
	if !ok {
		return fmt.Errorf("stuntgres: invalid trigger payload provider")
	}
	return list.Validate(read, d)
}
