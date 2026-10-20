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

func (*restoreReplay) ReplaySlot() {}

// operationReplay returns op's replay state, allocating it on first use.
func operationReplay(op *Operation) *restoreReplay {
	r, _ := op.replaySlot.(*restoreReplay)
	if r == nil {
		r = new(restoreReplay)
		op.replaySlot = r
	}
	return r
}

// operationReplayOrZero reads op's replay state; a nil slot reads as zero.
func operationReplayOrZero(op *Operation) restoreReplay {
	if r, _ := op.replaySlot.(*restoreReplay); r != nil {
		return *r
	}
	return restoreReplay{}
}
