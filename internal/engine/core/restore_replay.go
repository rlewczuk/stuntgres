package core

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
)

// restoreReplay is an operation's native Restore replay and materialized
// population state. Only Restore and materialized population write it, so
// ordinary statements keep a nil slot.
type restoreReplay struct {
	StagingEffects         bool               // Entire private Restore operation, including nested execution.
	TablespaceRepairs      *TablespaceRepairs // Explicit native OWNER/ACL fields only.
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
	GinInsert              *RestoreInsertCapture       // One annotated INSERT's actual final publication receipt.
	DomainDefault          *DomainDefaultReplayCapture // Paired CREATE only; no ordinary SQL authority.
	PLChecked              *PLCheckedReplayCapture     // Exact paired CREATE only, before its staged publication.
	// Exact decoded native membership reconstruction for one prepared statement.
	// Composition owns admission and clearing; ordinary SQL never sets these.
	ExtensionMembershipReplayRoot      ids.ObjectRef
	ExtensionMembershipReplayExtension ids.ObjectRef
	ExtensionMembershipReplayAction    string
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

// RestoreInsertCapture belongs to one annotated native INSERT in the isolated
// restore operation. Only the final physical publisher produces its identity.
// Count is saturated at two: anything other than exactly one insertion rejects.
type RestoreInsertCapture struct {
	Statement *parser.Insert
	Target    ids.ObjectRef
	Store     *storage.Relation
	row       ids.RowID
	version   ids.VersionID
	count     uint8
	invalid   bool
}

func (c *RestoreInsertCapture) record(ref ids.TxRef, address ids.ObjectAddress, store *storage.Relation, row ids.RowID, version ids.VersionID, insertion bool) {
	if c.count < 2 {
		c.count++
	}
	c.invalid = c.invalid || !insertion || c.Target.Domain() != ref.Domain() || c.Target.Epoch != ref.Epoch() || c.Target.Address != address || c.Store != store
	c.row, c.version = row, version
}

func (c *RestoreInsertCapture) Identity() (ids.RowID, ids.VersionID, bool) {
	if c == nil || c.invalid || c.count != 1 || c.row == 0 || c.version == 0 {
		return 0, 0, false
	}
	return c.row, c.version, true
}
