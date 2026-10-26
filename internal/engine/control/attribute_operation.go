package control

import "stuntgres/internal/engine/dump"

func attributeOperationEvent(op attributeOperation, index int) dump.Event {
	event := dump.Event{OID: op.Relation.ObjectID, Phase: dump.AttributeConstruction, Step: uint32(index + 1)}
	if op.Kind == 'c' && op.Relation.SubID == 0 {
		event.Phase, event.Step = dump.Declaration, 0
	}
	if op.Kind == 'd' {
		event.Phase = dump.AttributeRetirement
	}
	return event
}
