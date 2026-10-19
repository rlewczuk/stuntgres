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
	if node := reg.serializable.nodes[ref]; serializableRelevant(node) {
		return node
	}
	return nil
}

// serializableCreatorLocked is the serializable transaction that created
// version, or the zero ref. Caller holds Engine.mu.
func (reg *visibilityRegistry) serializableCreatorLocked(version ids.VersionID) ids.TxRef {
	return reg.serializable.creators[version]
}

// serializableReadLocked records node's read of an eligible relation and its
// rw-conflicts out to overlapping writers. Caller holds Engine.mu and has
// checked authority, epoch and eligibility.
func (reg *visibilityRegistry) serializableReadLocked(node *serializableTransaction, address ids.ObjectAddress) error {
	if node.doomed {
		return serializableError("Canceled on identification as a pivot, during conflict out checking.")
	}
	node.reads[address] = struct{}{}
	for _, writer := range reg.serializable.nodes {
		if !serializableOverlap(node, writer) {
			continue
		}
		for _, event := range writer.writes[address] {
			if event.deletion && event.baseBorn == 0 && event.creator == writer.ref {
				continue
			}
			selected := writer
			// For an invisible inserted predecessor, only its creator can be
			// the visibility conflict, even if another participant ended it.
			if event.base != 0 && event.baseBorn > node.frontier {
				selected = reg.serializable.nodes[event.creator]
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
		if !event.rewrite && (event.row == 0 || event.version == 0 || event.deletion && event.base != event.version) {
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
	if node.doomed {
		return serializableError("Canceled on identification as a pivot, during conflict in checking.")
	}
	if len(events) == 0 {
		return nil
	}
	node.didWrite = true
	for _, reader := range reg.serializable.nodes {
		if _, read := reader.reads[address]; read {
			if err := reg.serializableEdgeLocked(node, reader, node); err != nil {
				return err
			}
		}
	}
	writes := node.writes[address]
	if writes == nil {
		writes = make(map[serializableWriteKey]serializableWrite, len(events))
		node.writes[address] = writes
	}
	for _, event := range events {
		writes[serializableWriteKey{version: event.version, deletion: event.deletion}] = event
		if !event.deletion && !event.rewrite {
			reg.serializable.creators[event.version] = writer
		}
	}
	return nil
}

// Rollback restores surviving identity evidence only, never graph edges,
// predicates, doom or attempted-write history. Engine.mu and owner.mu held.
func (e *Engine) reconcileSerializableWritesLocked(t *transactionOwner) {
	e.visibility.reconcileSerializableWritesLocked(t.ref, func(address ids.ObjectAddress, event serializableWrite) bool {
		if event.rewrite {
			return t.catalog != nil && t.catalog.storage[address].rewritten
		}
		if event.deletion {
			root := t.deletes[address]
			if root == nil {
				return false
			}
			candidate, ok := root.rows[event.row]
			return ok && candidate.version == event.base
		}
		for _, row := range t.writes[address] {
			if row.Version == event.version {
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
	node := reg.serializable.nodes[ref]
	if node == nil {
		return
	}
	for address, events := range node.writes {
		for id, event := range events {
			if !keep(address, event) {
				delete(events, id)
				if !event.deletion && !event.rewrite && reg.serializable.creators[event.version] == ref {
					delete(reg.serializable.creators, event.version)
				}
			}
		}
		if len(events) == 0 {
			delete(node.writes, address)
		}
	}
}
