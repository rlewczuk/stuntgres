package table

import (
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
)

// PreserveTriggerModes composes an ordinary prepared structural replacement
// with mode edits already held by the same statement stage. Only surviving
// identities inherit state; fresh definitions keep their CREATE defaults.
func PreserveTriggerModes(source, next *catalog.Relation) *catalog.Relation {
	if source == nil || source == next {
		return next
	}
	var copy *catalog.Relation
	own := func() *catalog.Relation {
		if copy == nil {
			d := *next
			copy = &d
		}
		return copy
	}
	list := stored.TriggersOf(next)
	priorUsers := stored.TriggersOf(source)
	var userPositions map[ids.OID]int
	clonedTriggers := false
	for i, t := range list {
		at := i
		if at >= len(priorUsers) || priorUsers[at].OID() != t.OID() {
			if userPositions == nil {
				userPositions = make(map[ids.OID]int, len(priorUsers))
				for j, prior := range priorUsers { userPositions[prior.OID()] = j }
			}
			var found bool
			at, found = userPositions[t.OID()]
			if !found { continue }
		}
		prior := priorUsers[at]
		if prior.Mode() == t.Mode() { continue }
		if !clonedTriggers {
			list = slices.Clone(list)
			own().Triggers = stored.TriggersPayload(list)
			clonedTriggers = true
		}
		// Both values are installed checked modes.
		list[i], _ = t.WithMode(prior.Mode())
	}
	priorKeys := source.Constraints.ForeignKeys
	var keyPositions map[ids.OID]int
	clonedFK := false
	for i, fk := range next.Constraints.ForeignKeys {
		at := i
		if at >= len(priorKeys) || priorKeys[at].OID != fk.OID {
			if keyPositions == nil {
				keyPositions = make(map[ids.OID]int, len(priorKeys))
				for j, prior := range priorKeys { keyPositions[prior.OID] = j }
			}
			var found bool
			at, found = keyPositions[fk.OID]
			if !found { continue }
		}
		prior := priorKeys[at]
		if prior.TriggerOIDs != fk.TriggerOIDs || prior.TriggerModes == fk.TriggerModes { continue }
		d := own()
		if !clonedFK {
			d.Constraints.ForeignKeys = slices.Clone(next.Constraints.ForeignKeys)
			clonedFK = true
		}
		d.Constraints.ForeignKeys[i].TriggerModes = prior.TriggerModes
	}
	if copy != nil {
		return copy
	}
	return next
}
