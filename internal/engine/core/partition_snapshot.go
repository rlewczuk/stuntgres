package core

import "stuntgres/internal/engine/catalog"

// PendingDetachSnapshot exposes the existing stable snapshot catalog solely to
// current pending-edge eligibility. It is never permission or name authority.
// Like sequence metadata capture, callers hold Engine.Mu and statement admission.
func (s *StatementScope) PendingDetachSnapshot() *catalog.State {
	if s.Owner == nil || !s.Owner.SnapshotSet || !s.Owner.Isolation.Stable() {
		return nil
	}
	state := s.Owner.SequenceCatalog
	if state == nil || state.Epoch() != s.TxRef.Epoch() {
		return nil
	}
	return state
}
