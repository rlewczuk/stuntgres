package engine

import (
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/visibility"
)

// Partition integrity validation uses the grant-time committed frontier plus
// this owner's earlier commands, without changing its ordinary snapshot.
func acquirePartitionValidationViewsLocked(s *core.StatementScope, e *core.Engine, refs []ids.ObjectRef) (*visibility.View, error) {
	return s.CaptureDataViewsAtLocked(e, refs, true, nil)
}
