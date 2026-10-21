package engine

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
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
	partitionCheck         *catalog.Relation           // Exact leased private-import physical CHECK target.
	partitionUsingIndex    *partition.UsingIndexReplay // Exact leased private-import physical target/index.
	partitionUnique        *partition.UniqueReplay     // Exact private virtual definition/subtree lease.
	domainReplay           *domainReplayCapture        // Exact private native INSERT's immutable captured cells.
}

func (*restoreReplay) ReplaySlot() {}

// operationReplay returns op's replay state, allocating it on first use.
func operationReplay(op *session.Operation) *restoreReplay {
	r, _ := op.ReplaySlot.(*restoreReplay)
	if r == nil {
		r = new(restoreReplay)
		op.ReplaySlot = r
	}
	return r
}

// operationReplayOrZero reads op's replay state; a nil slot reads as zero.
func operationReplayOrZero(op *session.Operation) restoreReplay {
	if r, _ := op.ReplaySlot.(*restoreReplay); r != nil {
		return *r
	}
	return restoreReplay{}
}

// prepareRestoreReplayFlags sets the operation's replay flags for one
// admitted Restore statement. restoreRun.execute clears defaultReplay,
// partitionAttach and commentAuthority after execution; tableDestination
// and insertAuthority stay set until the next statement's prepare.
func prepareRestoreReplayFlags(_ *restoreRun, op *session.Operation, stmt parser.Statement) error {
	replay := operationReplay(op)
	replay.tableDestination = restoreTableDestination(stmt)
	_, replay.insertAuthority = stmt.(*parser.Insert)
	_, replay.commentAuthority = stmt.(*parser.Comment)
	_, replay.defaultReplay = stmt.(*parser.AlterTableDefault)
	_, replay.partitionAttach = stmt.(*parser.AlterTableAttachPartition)
	return nil
}

// restoreTableDestination reports the creations Restore replays with
// trusted importer provenance (the tableDestination flag).
func restoreTableDestination(stmt parser.Statement) bool {
	switch s := stmt.(type) {
	case *parser.CreateTable, *parser.CreateDomain, *parser.CreateEnum, *parser.CreateComposite,
		*parser.CreatePolicy, *parser.CreateRule, *parser.CreateSequence, *parser.CreateView,
		*parser.CreateIndex, *parser.CreateCollation, *parser.CreateConversion, *parser.AlterConversion,
		*parser.CreateOperator, *parser.AlterOperator, *parser.CreateExtension:
		return true
	case *parser.CreateQueryTable:
		return s.Form == parser.CreateMaterializedView
	}
	return false
}
