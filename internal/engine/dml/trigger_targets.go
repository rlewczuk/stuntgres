package dml

import (
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
)

// BeforeTargets borrows immutable binding/layout slices and owns their one-time
// physical attribute projection. It lives in the active command, not a cache
// attached to a prepared statement or a relation descriptor.
type BeforeTargets struct {
	Relation      ids.ObjectRef
	Slots, Layout []int
	Attributes    []int32
}

func UpdateTargetAttributes(h Host, ref ids.ObjectRef, relation *catalog.Relation, slots, layout []int) []int32 {
	command := h.Command()
	for _, capture := range command.BeforeTargets {
		if capture.Relation == ref && slices.Equal(capture.Slots, slots) && slices.Equal(capture.Layout, layout) {
			return capture.Attributes
		}
	}
	// PostgreSQL conservatively recomputes every generated column when any
	// BEFORE UPDATE row hook exists, including column-specific hooks themselves.
	// Capture those extra updated attributes once, never from per-row changes.
	generated := 0
	if len(relation.ColumnMeta.Generations) != 0 {
		for _, definition := range stored.TriggersOf(relation) {
			if p := definition.Parts(); p.Timing == stored.TriggerBefore && p.Level == stored.TriggerRow && p.Events&stored.TriggerUpdate != 0 {
				generated = len(relation.ColumnMeta.Generations)
				break
			}
		}
	}
	attributes := make([]int32, len(slots), len(slots)+len(relation.ColumnMeta.Generations))
	for i, slot := range slots {
		if layout != nil {
			slot = layout[slot]
		}
		attributes[i] = relation.Attributes[slot]
	}
	if generated != 0 {
		for _, attribute := range relation.Attributes {
			if _, exists := relation.ColumnMeta.Generations[attribute]; exists && !slices.Contains(attributes, attribute) {
				attributes = append(attributes, attribute)
			}
		}
	} else {
		for _, attribute := range relation.Attributes {
			definition, exists := relation.ColumnMeta.Generations[attribute]
			if !exists {
				continue
			}
			for _, dependency := range definition.Dependencies {
				address := dependency.Address
				number := address.SubID
				address.SubID = 0
				if address == relation.Address() && slices.Contains(attributes, number) {
					attributes = append(attributes, attribute)
					break
				}
			}
		}
	}
	command.BeforeTargets = append(command.BeforeTargets, BeforeTargets{Relation: ref, Slots: slots, Layout: layout, Attributes: attributes})
	return attributes
}
