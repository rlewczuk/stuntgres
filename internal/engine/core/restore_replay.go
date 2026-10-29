package core

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
)

// restoreReplay is an operation's native Restore replay and materialized
// population state. Only Restore and materialized population write it, so
// ordinary statements keep a nil slot.
type restoreReplay struct {
	StagingEffects         bool // Entire private Restore operation, including nested execution.
	TableDestination       bool
	InsertAuthority        bool
	CommentAuthority       bool
	LargeObjectAuthority   bool                       // Only exact generated private-stage byte replay.
	MaterializedPopulation bool                       // Operation-local execution path, never session settings.
	MaterializedOwner      authorization.PrincipalRef // Population-only effective principal; session_user is unchanged.
	DefaultReplay          bool
	SQLBodies              bool // Isolated Restore validator deferral, never a session setting.
	PartitionAttach        bool
	PartitionCheck         *catalog.Relation           // Exact leased private-import physical CHECK target.
	PartitionUsingIndex    *partition.UsingIndexReplay // Exact leased private-import physical target/index.
	PartitionUnique        *partition.UniqueReplay     // Exact private virtual definition/subtree lease.
	DomainReplay           *DomainReplayCapture        // Exact private native INSERT's immutable captured cells.
	DomainDefault          *DomainDefaultReplayCapture // Paired CREATE only; no ordinary SQL authority.
	PLChecked              *PLCheckedReplayCapture     // Exact paired CREATE only, before its staged publication.
}

func (*restoreReplay) ReplaySlot() {}

// OperationReplay returns op's replay state, allocating it on first use.
func OperationReplay(op *session.Operation) *restoreReplay {
	r, _ := op.ReplaySlot.(*restoreReplay)
	if r == nil {
		r = new(restoreReplay)
		op.ReplaySlot = r
	}
	return r
}

// OperationReplayOrZero reads op's replay state; a nil slot reads as zero.
func OperationReplayOrZero(op *session.Operation) restoreReplay {
	if r, _ := op.ReplaySlot.(*restoreReplay); r != nil {
		return *r
	}
	return restoreReplay{}
}

// CheckRestoreEffect guards an actual non-retractable capability, not function
// names, volatility or statement-local reconstruction authority.
func CheckRestoreEffect(op *session.Operation) error {
	if op != nil && OperationReplayOrZero(op).StagingEffects {
		return SQLError("0A000", "restore cannot stage non-retractable effects")
	}
	return nil
}

type DomainDefaultReplayCapture struct {
	Statement *parser.CreateDomain
	Record    typedef.DomainDefaultSnapshot
}

// PLCheckedReplayCapture leases genuine checked compilation, never permission to
// skip validation. Restore pairs this statement with its immediate owner transfer.
type PLCheckedReplayCapture struct {
	Statement *parser.CreateFunction
	Owner     authorization.PrincipalRef
	Consumed  bool
}
