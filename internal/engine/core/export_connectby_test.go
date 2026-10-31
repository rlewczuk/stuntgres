package core

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/visibility"
)

func CaptureConnectbySourceRoots(s *StatementScope, e *Engine, refs []ids.ObjectRef) (*visibility.View, error) {
	return s.captureDataRootsAtLocked(e, refs, false, nil, false)
}
