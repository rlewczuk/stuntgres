package engine

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
		if err == nil && (t.ref != s.txRef || t.ref.epoch != e.state.epoch) {
			err = ErrCatalogReplaced
		}
		s.op.session.mu.Unlock()
		if err != nil {
			e.mu.Unlock()
			return err
		}
		e.enrollSerializableLocked(t.ref, t.isolation)
		node := e.serializable.nodes[t.ref]
		node.captured = true
		node.readOnly = t.readOnly
		node.deferrable = t.deferrable
		node.frontier = e.commitSeq
		node.snapshotOrder = e.serializable.order
		node.resolved = false
		node.unsafe = false
		node.exempt = false
		node.possible = nil
		node.ready = nil
		roots := make(map[ObjectAddress]transactionRoot, len(e.storage))
		for address, storage := range e.storage {
			if relation, ok := e.state.descriptors[address].(*relationDescriptor); ok {
				roots[address] = transactionRoot{storage.versions, storage.ends, storage, relation}
			}
		}
		t.roots, t.frontier = roots, e.commitSeq
		t.sequenceCatalog = e.state
		if node.readOnly {
			for _, writer := range e.serializable.nodes {
				if writer != node && serializableRelevant(writer) && !serializableReadOnly(writer) && !writer.committed && writer.ref.epoch == node.ref.epoch {
					if node.possible == nil {
						node.possible = make(map[*serializableTransaction]struct{})
					}
					node.possible[writer] = struct{}{}
				}
			}
			if len(node.possible) == 0 {
				node.resolved = true
				node.exempt = true
			} else {
				node.ready = make(chan struct{})
			}
		}
		if !node.readOnly || !node.deferrable || node.resolved {
			t.snapshotSet = true
			e.mu.Unlock()
			return nil
		}
		ready := node.ready
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
		if err == nil && (t.ref != s.txRef || t.ref.epoch != e.state.epoch || node.aborted || node.ended) {
			err = ErrCatalogReplaced
		}
		s.op.session.mu.Unlock()
		if err != nil {
			node.possible = nil
			if !node.resolved {
				node.resolved = true
				close(node.ready)
			}
			t.roots = nil
			t.sequenceCatalog = nil
			t.frontier = 0
			e.mu.Unlock()
			return err
		}
		if node.resolved && !node.unsafe {
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
