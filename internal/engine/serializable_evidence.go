package engine

import (
	"errors"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

func (e *Engine) serializableEligibleLocked(ref ids.ObjectRef, changes *catalogChanges) bool {
	d, ok := e.privateCatalogLocked(changes).Relation(ref.Address)
	if !ok || d.RelationKind != catalog.TableRelation {
		return false
	}
	ns, ok := e.privateCatalogLocked(changes).Namespace(d.Namespace())
	return ok && ns.Temporary == (ids.SessionToken{}) && d.Namespace().ObjectID != ids.PGCatalogNamespaceOID && e.storage.Relations[ref.Address] != nil
}

func (e *Engine) serializableReadLocked(s *StatementScope, ref ids.ObjectRef) error {
	if ref.Domain() != e.catalog.Domain || ref.Address.SubID != 0 {
		return errors.New("stuntgres: foreign SSI relation")
	}
	if ref.Epoch != e.catalog.state.Epoch() || ref.Epoch != s.txRef.Epoch() {
		return ids.ErrCatalogReplaced
	}
	if s.txRef.Domain() != e.catalog.Domain || txOwnerOf(s.txRef) != s.owner || s.txRef != s.owner.ref {
		return errors.New("stuntgres: invalid SSI read authority")
	}
	// Relevance is the cheap test; most readers are not serializable.
	node := e.visibility.relevantSerializableLocked(s.txRef)
	if node == nil || !e.serializableEligibleLocked(ref, s.owner.catalog) {
		return nil
	}
	return e.visibility.serializableReadLocked(node, ref.Address)
}

// relevantSerializableLocked returns ref's captured, live, non-exempt tracker
// node, or nil. Caller holds Engine.mu.
func (reg *visibilityRegistry) relevantSerializableLocked(ref ids.TxRef) *serializableTransaction {
	if node := reg.Serializable.Nodes[ref]; serializableRelevant(node) {
		return node
	}
	return nil
}

// serializableCreatorLocked is the serializable transaction that created
// version, or the zero ref. Caller holds Engine.mu.
func (reg *visibilityRegistry) serializableCreatorLocked(version ids.VersionID) ids.TxRef {
	return reg.Serializable.Creators[version]
}

// serializableReadLocked records node's read of an eligible relation and its
// rw-conflicts out to overlapping writers. Caller holds Engine.mu and has
// checked authority, epoch and eligibility.
func (reg *visibilityRegistry) serializableReadLocked(node *serializableTransaction, address ids.ObjectAddress) error {
	if node.Doomed {
		return serializableError("Canceled on identification as a pivot, during conflict out checking.")
	}
	node.Reads[address] = struct{}{}
	for _, writer := range reg.Serializable.Nodes {
		if !serializableOverlap(node, writer) {
			continue
		}
		for _, event := range writer.Writes[address] {
			if event.Deletion && event.BaseBorn == 0 && event.Creator == writer.Ref {
				continue
			}
			selected := writer
			// For an invisible inserted predecessor, only its creator can be
			// the visibility conflict, even if another participant ended it.
			if event.Base != 0 && event.BaseBorn > node.Frontier {
				selected = reg.Serializable.Nodes[event.Creator]
			}
			if err := reg.serializableEdgeLocked(node, node, selected); err != nil {
				return err
			}
		}
	}
	return nil
}

func (e *Engine) serializableWriteLocked(s *StatementScope, ref ids.ObjectRef, events []serializableWrite) error {
	if ref.Domain() != e.catalog.Domain || ref.Address.SubID != 0 || s.txRef.Domain() != e.catalog.Domain || txOwnerOf(s.txRef) != s.owner || s.txRef != s.owner.ref {
		return errors.New("stuntgres: invalid SSI write authority")
	}
	if ref.Epoch != e.catalog.state.Epoch() || ref.Epoch != s.txRef.Epoch() {
		return ids.ErrCatalogReplaced
	}
	for _, event := range events {
		if !event.Rewrite && (event.Row == 0 || event.Version == 0 || event.Deletion && event.Base != event.Version) {
			return errors.New("stuntgres: invalid SSI write identity")
		}
	}
	if !e.serializableEligibleLocked(ref, s.owner.catalog) {
		return nil
	}
	node := e.visibility.relevantSerializableLocked(s.txRef)
	if node == nil {
		return nil
	}
	return e.visibility.serializableWriteLocked(node, s.txRef, ref.Address, events)
}

// serializableWriteLocked records node's validated write events on an
// eligible relation, its rw-conflicts in from earlier readers, and writer
// as the creator of each inserted version. Caller holds Engine.mu and has checked
// authority, epoch, event identities and eligibility.
func (reg *visibilityRegistry) serializableWriteLocked(node *serializableTransaction, writer ids.TxRef, address ids.ObjectAddress, events []serializableWrite) error {
	if node.Doomed {
		return serializableError("Canceled on identification as a pivot, during conflict in checking.")
	}
	if len(events) == 0 {
		return nil
	}
	node.DidWrite = true
	for _, reader := range reg.Serializable.Nodes {
		if _, read := reader.Reads[address]; read {
			if err := reg.serializableEdgeLocked(node, reader, node); err != nil {
				return err
			}
		}
	}
	writes := node.Writes[address]
	if writes == nil {
		writes = make(map[serializableWriteKey]serializableWrite, len(events))
		node.Writes[address] = writes
	}
	for _, event := range events {
		writes[serializableWriteKey{Version: event.Version, Deletion: event.Deletion}] = event
		if !event.Deletion && !event.Rewrite {
			reg.Serializable.Creators[event.Version] = writer
		}
	}
	return nil
}

// Rollback restores surviving identity evidence only, never graph edges,
// predicates, doom or attempted-write history. Engine.mu and owner.mu held.
func (e *Engine) reconcileSerializableWritesLocked(t *transactionOwner) {
	e.visibility.reconcileSerializableWritesLocked(t.ref, func(address ids.ObjectAddress, event serializableWrite) bool {
		if event.Rewrite {
			return t.catalog != nil && t.catalog.storage[address].rewritten
		}
		if event.Deletion {
			root := t.deletes[address]
			if root == nil {
				return false
			}
			candidate, ok := root.Rows[event.Row]
			return ok && candidate.Version == event.Base
		}
		for _, row := range t.writes[address] {
			if row.Version == event.Version {
				return true
			}
		}
		return false
	})
}

// reconcileSerializableWritesLocked drops ref's write events that keep
// rejects, and the creator records of dropped insertions. keep reads only
// the caller's transaction state: it takes no lock and never calls back into
// the registry. Caller holds Engine.mu.
func (reg *visibilityRegistry) reconcileSerializableWritesLocked(ref ids.TxRef, keep func(ids.ObjectAddress, serializableWrite) bool) {
	node := reg.Serializable.Nodes[ref]
	if node == nil {
		return
	}
	for address, events := range node.Writes {
		for id, event := range events {
			if !keep(address, event) {
				delete(events, id)
				if !event.Deletion && !event.Rewrite && reg.Serializable.Creators[event.Version] == ref {
					delete(reg.Serializable.Creators, event.Version)
				}
			}
		}
		if len(events) == 0 {
			delete(node.Writes, address)
		}
	}
}
