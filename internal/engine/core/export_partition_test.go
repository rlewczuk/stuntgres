package core

import (
	"stuntgres/internal/engine/catalog"
)

// ScopeCapturePartitionInsertLocked calls x.capturePartitionInsertLocked.
func ScopeCapturePartitionInsertLocked(x *StatementScope, e *Engine, auth AuthorizationView, target *catalog.Relation) error {
	return x.capturePartitionInsertLocked(e, auth, target)
}
