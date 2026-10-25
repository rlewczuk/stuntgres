package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

// PartitionReplayAllowed calls partitionReplayAllowed.
func PartitionReplayAllowed(op *session.Operation, auth AuthorizationView) func(parser.Statement, *catalog.Relation) bool {
	return partitionReplayAllowed(op, auth)
}

// ScopeCapturePartitionInsertLocked calls x.capturePartitionInsertLocked.
func ScopeCapturePartitionInsertLocked(x *StatementScope, e *Engine, auth AuthorizationView, target *catalog.Relation) error {
	return x.capturePartitionInsertLocked(e, auth, target)
}
