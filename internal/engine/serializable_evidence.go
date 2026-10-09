package engine

import "errors"

func (e *Engine) serializableEligibleLocked(ref ObjectRef, catalog *catalogChanges) bool {
	d, ok := e.privateCatalogLocked(catalog).state.descriptors[ref.Address].(*relationDescriptor)
	if !ok || d.relationKind != tableRelation {
		return false
	}
	ns, ok := e.privateCatalogLocked(catalog).state.descriptors[d.namespace].(*namespaceDescriptor)
	return ok && ns.temporary == (SessionToken{}) && d.namespace.ObjectID != PGCatalogNamespaceOID && e.storage[ref.Address] != nil
}

func (e *Engine) serializableReadLocked(s *StatementScope, ref ObjectRef) error {
	if ref.owner != e.owner || ref.Address.SubID != 0 {
		return errors.New("stuntgres: foreign SSI relation")
	}
	if ref.Epoch != e.state.epoch || ref.Epoch != s.txRef.epoch {
		return ErrCatalogReplaced
	}
	if s.txRef.owner != e || s.txRef.attachment != s.owner || s.txRef != s.owner.ref {
		return errors.New("stuntgres: invalid SSI read authority")
	}
	node := e.serializable.nodes[s.txRef]
	if !serializableRelevant(node) || !e.serializableEligibleLocked(ref, s.owner.catalog) {
		return nil
	}
	if node.doomed {
		return serializableError("Canceled on identification as a pivot, during conflict out checking.")
	}
	node.reads[ref.Address] = struct{}{}
	for _, writer := range e.serializable.nodes {
		if !serializableOverlap(node, writer) {
			continue
		}
		for _, event := range writer.writes[ref.Address] {
			if event.deletion && event.baseBorn == 0 && event.creator == writer.ref {
				continue
			}
			selected := writer
			// For an invisible inserted predecessor, only its creator can be
			// the visibility conflict, even if another participant ended it.
			if event.base != 0 && event.baseBorn > node.frontier {
				selected = e.serializable.nodes[event.creator]
			}
			if err := e.serializableEdgeLocked(node, node, selected); err != nil {
				return err
			}
		}
	}
	return nil
}

func (e *Engine) serializableWriteLocked(s *StatementScope, ref ObjectRef, events []serializableWrite) error {
	if ref.owner != e.owner || ref.Address.SubID != 0 || s.txRef.owner != e || s.txRef.attachment != s.owner || s.txRef != s.owner.ref {
		return errors.New("stuntgres: invalid SSI write authority")
	}
	if ref.Epoch != e.state.epoch || ref.Epoch != s.txRef.epoch {
		return ErrCatalogReplaced
	}
	for _, event := range events {
		if !event.rewrite && (event.row == 0 || event.version == 0 || event.deletion && event.base != event.version) {
			return errors.New("stuntgres: invalid SSI write identity")
		}
	}
	if !e.serializableEligibleLocked(ref, s.owner.catalog) {
		return nil
	}
	node := e.serializable.nodes[s.txRef]
	if !serializableRelevant(node) {
		return nil
	}
	if node.doomed {
		return serializableError("Canceled on identification as a pivot, during conflict in checking.")
	}
	if len(events) == 0 {
		return nil
	}
	node.didWrite = true
	for _, reader := range e.serializable.nodes {
		if _, read := reader.reads[ref.Address]; read {
			if err := e.serializableEdgeLocked(node, reader, node); err != nil {
				return err
			}
		}
	}
	writes := node.writes[ref.Address]
	if writes == nil {
		writes = make(map[serializableWriteKey]serializableWrite, len(events))
		node.writes[ref.Address] = writes
	}
	for _, event := range events {
		writes[serializableWriteKey{version: event.version, deletion: event.deletion}] = event
		if !event.deletion && !event.rewrite {
			e.serializable.creators[event.version] = s.txRef
		}
	}
	return nil
}

// Rollback restores surviving identity evidence only, never graph edges,
// predicates, doom or attempted-write history. Engine.mu and owner.mu held.
func (e *Engine) reconcileSerializableWritesLocked(t *transactionOwner) {
	node := e.serializable.nodes[t.ref]
	if node == nil {
		return
	}
	for address, events := range node.writes {
		for id, event := range events {
			keep := false
			if event.rewrite {
				keep = t.catalog != nil && t.catalog.storage[address].rewritten
			} else if event.deletion {
				root := t.deletes[address]
				if root != nil {
					candidate, ok := root.rows[event.row]
					keep = ok && candidate.version == event.base
				}
			} else {
				for _, row := range t.writes[address] {
					if row.version == event.version {
						keep = true
						break
					}
				}
			}
			if !keep {
				delete(events, id)
				if !event.deletion && !event.rewrite && e.serializable.creators[event.version] == t.ref {
					delete(e.serializable.creators, event.version)
				}
			}
		}
		if len(events) == 0 {
			delete(node.writes, address)
		}
	}
}
