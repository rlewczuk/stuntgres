// Package dump owns the records native Dump orders and emits: the event
// graph, its topological order, the statement and group records, and the
// Restore scalar validators. It holds no state and no lock.
package dump

import (
	"slices"

	"stuntgres/internal/engine/ids"
)

// Phase orders the events of one object. The numeric order is part of the
// Order tie-break, so new phases go at the end.
type Phase uint8

const (
	Declaration Phase = iota
	Rename
	Defaults
	Rows
	InvalidConstraints
	IndexAttachment
	TableAttachment
	TableCompletion
	InvalidIndexPreparation
	InvalidIndexCompletion
	InheritanceRecipes
	InheritanceChecks
	RetiredConstraints
	InheritedForeignKeys
	DomainChecks
	OperatorLinks
)

// Event is one emission step: a phase of the object with this OID.
type Event struct {
	OID   ids.OID
	Phase Phase
}

// Graph maps each event to the events that must be emitted before it.
type Graph map[Event][]Event

// Add records predecessor before event once.
func (g Graph) Add(event, predecessor Event) {
	if !slices.Contains(g[event], predecessor) {
		g[event] = append(g[event], predecessor)
	}
}

// DependsOn reports whether event reaches target through predecessors.
func (g Graph) DependsOn(event, target Event, seen map[Event]bool) bool {
	if event == target {
		return true
	}
	if seen[event] {
		return false
	}
	seen[event] = true
	for _, predecessor := range g[event] {
		if g.DependsOn(predecessor, target, seen) {
			return true
		}
	}
	return false
}

// DeclarationEvents returns the declaration event of each OID, in order.
func DeclarationEvents(ids []ids.OID) []Event {
	events := make([]Event, len(ids))
	for i, oid := range ids {
		events[i] = Event{OID: oid}
	}
	return events
}

// DeclarationIDs returns the OIDs of the declaration events, in order.
func DeclarationIDs(events []Event) []ids.OID {
	ids := make([]ids.OID, 0, len(events))
	for _, event := range events {
		if event.Phase == Declaration {
			ids = append(ids, event.OID)
		}
	}
	return ids
}
