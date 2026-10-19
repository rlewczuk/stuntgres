package visibility

import (
	"stuntgres/internal/engine/ids"
)

// RelevantSerializableLocked returns ref's captured, live, non-exempt tracker
// node, or nil. Caller holds Engine.mu.
func (reg *Registry) RelevantSerializableLocked(ref ids.TxRef) *SerializableTx {
	if node := reg.Serializable.Nodes[ref]; serializableRelevant(node) {
		return node
	}
	return nil
}

// SerializableCreatorLocked is the serializable transaction that created
// version, or the zero ref. Caller holds Engine.mu.
func (reg *Registry) SerializableCreatorLocked(version ids.VersionID) ids.TxRef {
	return reg.Serializable.Creators[version]
}

// SerializableReadLocked records node's read of an eligible relation and its
// rw-conflicts out to overlapping writers. Caller holds Engine.mu and has
// checked authority, epoch and eligibility.
func (reg *Registry) SerializableReadLocked(node *SerializableTx, address ids.ObjectAddress) error {
	if node.Doomed {
		return serializableError("Canceled on identification as a pivot, during conflict out checking.")
	}
	node.Reads[address] = struct{}{}
	for _, writer := range reg.Serializable.Nodes {
		if !SerializableOverlap(node, writer) {
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
			if err := reg.SerializableEdgeLocked(node, node, selected); err != nil {
				return err
			}
		}
	}
	return nil
}

// SerializableWriteLocked records node's validated write events on an
// eligible relation, its rw-conflicts in from earlier readers, and writer
// as the creator of each inserted version. Caller holds Engine.mu and has checked
// authority, epoch, event identities and eligibility.
func (reg *Registry) SerializableWriteLocked(node *SerializableTx, writer ids.TxRef, address ids.ObjectAddress, events []SerializableWrite) error {
	if node.Doomed {
		return serializableError("Canceled on identification as a pivot, during conflict in checking.")
	}
	if len(events) == 0 {
		return nil
	}
	node.DidWrite = true
	for _, reader := range reg.Serializable.Nodes {
		if _, read := reader.Reads[address]; read {
			if err := reg.SerializableEdgeLocked(node, reader, node); err != nil {
				return err
			}
		}
	}
	writes := node.Writes[address]
	if writes == nil {
		writes = make(map[WriteKey]SerializableWrite, len(events))
		node.Writes[address] = writes
	}
	for _, event := range events {
		writes[WriteKey{Version: event.Version, Deletion: event.Deletion}] = event
		if !event.Deletion && !event.Rewrite {
			reg.Serializable.Creators[event.Version] = writer
		}
	}
	return nil
}

// ReconcileSerializableWritesLocked drops ref's write events that keep
// rejects, and the creator records of dropped insertions. keep reads only
// the caller's transaction state: it takes no lock and never calls back into
// the registry. Caller holds Engine.mu.
func (reg *Registry) ReconcileSerializableWritesLocked(ref ids.TxRef, keep func(ids.ObjectAddress, SerializableWrite) bool) {
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
