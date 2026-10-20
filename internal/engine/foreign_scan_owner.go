package engine

import "stuntgres/internal/engine/exec"

func (s *StatementScope) ownForeignSource(source *exec.ForeignQuerySource) {
	owner := &s.foreignTables().owner
	if s.foreignTablesOrZero().cursor != nil {
		owner = &s.foreignTables().cursor.foreignOwner
	}
	if *owner == nil {
		*owner = &exec.ForeignScanOwner{}
	}
	(*owner).Add(source)
}

// The caller retains admission but holds no engine/transaction/session mutex.
// A primary operation failure is never replaced by cleanup failure.
func (s *StatementScope) finishForeignOwner(owner *exec.ForeignScanOwner, primary error) error {
	if owner == nil {
		return primary
	}
	if s.replacementRead {
		s.op.session.Engine().replacementMu.RUnlock()
		s.replacementRead = false
	}
	cleanup := owner.Finish(s.op.Context())
	if primary != nil {
		return primary
	}
	return cleanup
}
