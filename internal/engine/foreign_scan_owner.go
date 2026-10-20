package engine

func (s *StatementScope) ownForeignSource(source *foreignQuerySource) {
	owner := &s.foreignTables().owner
	if s.foreignTablesOrZero().cursor != nil {
		owner = &s.foreignTables().cursor.foreignOwner
	}
	if *owner == nil {
		*owner = &foreignScanOwner{}
	}
	(*owner).Add(source)
}

// The caller retains admission but holds no engine/transaction/session mutex.
// A primary operation failure is never replaced by cleanup failure.
func (s *StatementScope) finishForeignOwner(owner *foreignScanOwner, primary error) error {
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
