package engine

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
)

// Caller holds the admitted owner's mutex, never a peer owner. Waiting releases
// Engine.mu and the implicit replacement fence; terminal session cancellation
// can therefore drain this operation and replacement can fence its candidate.
func (s *StatementScope) AcquireSerializableSnapshot() error {
	t, e := s.Owner, s.Engine()
	if t.SnapshotSet || t.Isolation != session.Serializable {
		return nil
	}
	for {
		e.Mu.Lock()
		s.Op.Session.Mu.Lock()
		err := PublicationOperationLocked(e, s.Op)
		if err == nil && (t.Ref != s.TxRef || t.Ref.Epoch() != e.Catalog.State.Epoch()) {
			err = ids.ErrCatalogReplaced
		}
		s.Op.Session.Mu.Unlock()
		if err != nil {
			e.Mu.Unlock()
			return err
		}
		if err := e.retainTransactionSnapshotLocked(t); err != nil {
			e.Mu.Unlock()
			return err
		}
		e.enrollSerializableLocked(t.Ref, t.Isolation)
		node := e.Visibility.CaptureSerializableLocked(t.Ref, t.ReadOnly, t.Deferrable)
		ready, wait := node.SafeSnapshotWait()
		if !wait {
			t.SnapshotSet = true
			e.Mu.Unlock()
			return nil
		}
		e.Mu.Unlock()
		if s.ReplacementRead {
			e.ReplacementMu.RUnlock()
			s.ReplacementRead = false
		}
		select {
		case <-ready:
		case <-s.Op.Context().Done():
		}
		if s.implicit {
			e.ReplacementMu.RLock()
			s.ReplacementRead = true
		}
		e.Mu.Lock()
		s.Op.Session.Mu.Lock()
		err = PublicationOperationLocked(e, s.Op)
		live, safe := node.SafeSnapshotOutcome()
		if err == nil && (t.Ref != s.TxRef || t.Ref.Epoch() != e.Catalog.State.Epoch() || !live) {
			err = ids.ErrCatalogReplaced
		}
		s.Op.Session.Mu.Unlock()
		if err != nil {
			node.AbandonSafeSnapshotLocked()
			e.Visibility.DropSnapshotLocked(t.Ref)
			t.Roots = nil
			t.SequenceCatalog = nil
			t.CurrentSnapshot = nil
			t.Frontier = 0
			e.Mu.Unlock()
			return err
		}
		if safe {
			t.SnapshotSet = true
			e.Mu.Unlock()
			return nil
		}
		// Unsafe candidates have exposed neither rows nor analysis handles.
		// Discard their immutable roots and take a fresh authoritative snapshot.
		t.Roots = nil
		t.SequenceCatalog = nil
		t.CurrentSnapshot = nil
		t.Frontier = 0
		e.Mu.Unlock()
	}
}
