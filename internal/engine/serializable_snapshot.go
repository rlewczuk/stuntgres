package engine

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/visibility"
)

// Caller holds the admitted owner's mutex, never a peer owner. Waiting releases
// Engine.mu and the implicit replacement fence; terminal session cancellation
// can therefore drain this operation and replacement can fence its candidate.
func (s *StatementScope) acquireSerializableSnapshot() error {
	t, e := s.owner, s.engine()
	if t.SnapshotSet || t.Isolation != Serializable {
		return nil
	}
	for {
		e.mu.Lock()
		s.op.Session.Mu.Lock()
		err := publicationOperationLocked(e, s.op)
		if err == nil && (t.Ref != s.txRef || t.Ref.Epoch() != e.catalog.state.Epoch()) {
			err = ids.ErrCatalogReplaced
		}
		s.op.Session.Mu.Unlock()
		if err != nil {
			e.mu.Unlock()
			return err
		}
		e.enrollSerializableLocked(t.Ref, t.Isolation)
		node := e.visibility.CaptureSerializableLocked(t.Ref, t.ReadOnly, t.Deferrable)
		roots := make(map[ids.ObjectAddress]visibility.Root, len(e.storage.Relations))
		for address, storage := range e.storage.Relations {
			if relation, ok := e.catalog.state.Relation(address); ok {
				roots[address] = visibility.Root{storage.Versions, storage.Ends, storage, relation}
			}
		}
		t.Roots, t.Frontier = roots, e.visibility.CommitSeq()
		t.SequenceCatalog = e.catalog.state
		ready, wait := node.SafeSnapshotWait()
		if !wait {
			t.SnapshotSet = true
			e.mu.Unlock()
			return nil
		}
		e.mu.Unlock()
		if s.replacementRead {
			e.replacementMu.RUnlock()
			s.replacementRead = false
		}
		select {
		case <-ready:
		case <-s.op.Context().Done():
		}
		if s.implicit {
			e.replacementMu.RLock()
			s.replacementRead = true
		}
		e.mu.Lock()
		s.op.Session.Mu.Lock()
		err = publicationOperationLocked(e, s.op)
		live, safe := node.SafeSnapshotOutcome()
		if err == nil && (t.Ref != s.txRef || t.Ref.Epoch() != e.catalog.state.Epoch() || !live) {
			err = ids.ErrCatalogReplaced
		}
		s.op.Session.Mu.Unlock()
		if err != nil {
			node.AbandonSafeSnapshotLocked()
			t.Roots = nil
			t.SequenceCatalog = nil
			t.Frontier = 0
			e.mu.Unlock()
			return err
		}
		if safe {
			t.SnapshotSet = true
			e.mu.Unlock()
			return nil
		}
		// Unsafe candidates have exposed neither rows nor analysis handles.
		// Discard their immutable roots and take a fresh authoritative snapshot.
		t.Roots = nil
		t.SequenceCatalog = nil
		t.Frontier = 0
		e.mu.Unlock()
	}
}
