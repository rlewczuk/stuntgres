package stored

import (
	"fmt"
	"slices"
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
		remove := slices.Contains(targets, ids.ObjectAddress{ClassID: 2620, ObjectID: t.OID()}) || slices.Contains(targets, t.parts.Parent)
		for _, target := range targets {
			if TriggerDepends(t, target) || slices.Contains(t.parts.AutoExtensions, target) {
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

// TriggerParent finds the owning relation of an exact attached trigger identity.
func TriggerParent(read catalog.Read, address ids.ObjectAddress) (*catalog.Relation, Trigger) {
	for _, d := range read.Descriptors() {
		if relation, ok := d.(*catalog.Relation); ok {
			for _, t := range TriggersOf(relation) {
				if address == (ids.ObjectAddress{ClassID: 2620, ObjectID: t.OID()}) {
					return relation, t
				}
			}
		}
	}
	return nil, Trigger{}
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
