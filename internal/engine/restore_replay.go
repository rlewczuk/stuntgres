package engine

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
)

// restoreReplay is an operation's native Restore replay and materialized
// population state. Only Restore and materialized population write it, so
// ordinary statements keep a nil slot.
type restoreReplay struct {
	tableDestination       bool
	insertAuthority        bool
	commentAuthority       bool
	materializedPopulation bool                       // Operation-local execution path, never session settings.
	materializedOwner      authorization.PrincipalRef // Population-only effective principal; session_user is unchanged.
	defaultReplay          bool
	sqlBodies              bool // Isolated Restore validator deferral, never a session setting.
	partitionAttach        bool
	partitionCheck         *catalog.Relation          // Exact leased private-import physical CHECK target.
	partitionUsingIndex    *partitionUsingIndexReplay // Exact leased private-import physical target/index.
	partitionUnique        *partitionUniqueReplay     // Exact private virtual definition/subtree lease.
	domainReplay           *domainReplayCapture       // Exact private native INSERT's immutable captured cells.
}

// replay returns the replay slot, allocating it on first use.
func (op *Operation) replay() *restoreReplay {
	if op.replaySlot == nil {
		op.replaySlot = new(restoreReplay)
	}
	return op.replaySlot
}

// replayOrZero reads the replay slot; a nil slot reads as zero.
func (op *Operation) replayOrZero() restoreReplay {
	if op.replaySlot == nil {
		return restoreReplay{}
	}
	return *op.replaySlot
}
