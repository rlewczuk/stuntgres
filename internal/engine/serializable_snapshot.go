package engine

import "stuntgres/internal/engine/ids"

// Caller holds the admitted owner's mutex, never a peer owner. Waiting releases
// Engine.mu and the implicit replacement fence; terminal session cancellation
// can therefore drain this operation and replacement can fence its candidate.
func (s *StatementScope) acquireSerializableSnapshot() error {
	t, e := s.owner, s.op.session.Engine()
	if t.snapshotSet || t.isolation != Serializable {
		return nil
	}
	for {
		e.mu.Lock()
		s.op.session.mu.Lock()
		err := publicationOperationLocked(e, s.op)
		if err == nil && (t.ref != s.txRef || t.ref.Epoch() != e.catalog.state.Epoch()) {
			err = ids.ErrCatalogReplaced
		}
		s.op.session.mu.Unlock()
		if err != nil {
			e.mu.Unlock()
			return err
		}
		e.enrollSerializableLocked(t.ref, t.isolation)
		node := e.visibility.captureSerializableLocked(t.ref, t.readOnly, t.deferrable)
		roots := make(map[ids.ObjectAddress]transactionRoot, len(e.storage.Relations))
		for address, storage := range e.storage.Relations {
			if relation, ok := e.catalog.state.Relation(address); ok {
				roots[address] = transactionRoot{storage.Versions, storage.Ends, storage, relation}
			}
		}
		t.roots, t.frontier = roots, e.visibility.commitSeq
		t.sequenceCatalog = e.catalog.state
		if !node.ReadOnly || !node.Deferrable || node.Resolved {
			t.snapshotSet = true
			e.mu.Unlock()
			return nil
		}
		ready := node.Ready
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
		s.op.session.mu.Lock()
		err = publicationOperationLocked(e, s.op)
		if err == nil && (t.ref != s.txRef || t.ref.Epoch() != e.catalog.state.Epoch() || node.Aborted || node.Ended) {
			err = ids.ErrCatalogReplaced
		}
		s.op.session.mu.Unlock()
		if err != nil {
			node.Possible = nil
			if !node.Resolved {
				node.Resolved = true
				close(node.Ready)
			}
			t.roots = nil
			t.sequenceCatalog = nil
			t.frontier = 0
			e.mu.Unlock()
			return err
		}
		if node.Resolved && !node.Unsafe {
			t.snapshotSet = true
			e.mu.Unlock()
			return nil
		}
		// Unsafe candidates have exposed neither rows nor analysis handles.
		// Discard their immutable roots and take a fresh authoritative snapshot.
		t.roots = nil
		t.sequenceCatalog = nil
		t.frontier = 0
		e.mu.Unlock()
	}
}

// captureSerializableLocked marks ref's enrolled node captured at the current
// commit frontier and finalization order. A read-only node collects the
// overlapping read-write writers it must wait out; with none it is exempt
// at once, otherwise it gets a ready channel that resolution closes. Caller
// holds Engine.mu and has enrolled ref.
func (reg *visibilityRegistry) captureSerializableLocked(ref ids.TxRef, readOnly, deferrable bool) *serializableTransaction {
	node := reg.Serializable.Nodes[ref]
	node.Captured = true
	node.ReadOnly = readOnly
	node.Deferrable = deferrable
	node.Frontier = reg.commitSeq
	node.SnapshotOrder = reg.Serializable.order
	node.Resolved = false
	node.Unsafe = false
	node.Exempt = false
	node.Possible = nil
	node.Ready = nil
	if node.ReadOnly {
		for _, writer := range reg.Serializable.Nodes {
			if writer != node && serializableRelevant(writer) && !serializableReadOnly(writer) && !writer.Committed && writer.Ref.Epoch() == node.Ref.Epoch() {
				if node.Possible == nil {
					node.Possible = make(map[*serializableTransaction]struct{})
				}
				node.Possible[writer] = struct{}{}
			}
		}
		if len(node.Possible) == 0 {
			node.Resolved = true
			node.Exempt = true
		} else {
			node.Ready = make(chan struct{})
		}
	}
	return node
}
