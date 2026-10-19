package visibility

import (
	"errors"
	"fmt"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
)

// Tracker is the serializable (SSI) dependency graph. Engine.mu owns the
// graph, candidates and final publication order.
// No graph operation takes a peer transaction's mutex. Records outlive their
// session while overlapping owners remain; visibility roots stay TX-owned.
type Tracker struct {
	Nodes    map[ids.TxRef]*SerializableTx
	Creators map[ids.VersionID]ids.TxRef
	order    uint64
}

// SerializableTx is one serializable transaction's tracker node.
type SerializableTx struct {
	Ref                                                 ids.TxRef
	AssignedXID                                         uint32 // Retained canonical public identity, including after completion.
	Captured, ReadOnly, Deferrable                      bool
	Frontier                                            ids.CommitSeq
	SnapshotOrder, Order                                uint64
	CommitFrontier                                      ids.CommitSeq
	CommitOrder                                         uint64
	DidWrite, Doomed, Committed, Ended, Aborted, Exempt bool
	Finalized, Prepared                                 bool
	Reads                                               map[ids.ObjectAddress]struct{}
	Writes                                              map[ids.ObjectAddress]map[WriteKey]SerializableWrite
	Incoming, Outgoing                                  map[*SerializableTx]struct{}
	Possible                                            map[*SerializableTx]struct{}
	Ready                                               chan struct{}
	Resolved, Unsafe                                    bool
}

// A retained event contains identities, never row values. baseBorn and creator
// select the invisible creator rather than an ender of that invisible version.
type SerializableWrite struct {
	Row               ids.RowID
	Version, Base     ids.VersionID
	BaseBorn          ids.CommitSeq
	Creator           ids.TxRef
	Deletion, Rewrite bool
}

// Creation and ending of the same version survive/reconcile independently.
// The zero version is reserved for the relation-wide rewrite event.
type WriteKey struct {
	Version  ids.VersionID
	Deletion bool
}

func serializableError(detail string) error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "40001", Message: "could not serialize access due to read/write dependencies among transactions", Detail: "Reason code: " + detail, Hint: "The transaction might succeed if retried."}
}

// EnrollSerializableLocked creates ref's tracker node for a serializable
// transaction and drops an uncaptured one otherwise. A captured node keeps
// its snapshot's mode. Caller holds Engine.mu and has fenced ref.
func (reg *Registry) EnrollSerializableLocked(ref ids.TxRef, serializable bool) {
	node := reg.Serializable.Nodes[ref]
	if node != nil && node.Captured {
		return
	}
	if !serializable {
		delete(reg.Serializable.Nodes, ref)
		return
	}
	if node == nil {
		if reg.Serializable.Nodes == nil {
			reg.Serializable.Nodes = make(map[ids.TxRef]*SerializableTx)
		}
		if reg.Serializable.Creators == nil {
			reg.Serializable.Creators = make(map[ids.VersionID]ids.TxRef)
		}
		reg.Serializable.Nodes[ref] = &SerializableTx{Ref: ref, AssignedXID: reg.Entries[ref].AssignedXID, Reads: make(map[ids.ObjectAddress]struct{}), Writes: make(map[ids.ObjectAddress]map[WriteKey]SerializableWrite), Incoming: make(map[*SerializableTx]struct{}), Outgoing: make(map[*SerializableTx]struct{})}
	}
}

func serializableRelevant(node *SerializableTx) bool {
	return node != nil && node.Captured && !node.Aborted && !node.Exempt
}

func serializableReadOnly(node *SerializableTx) bool {
	return !node.DidWrite && (node.ReadOnly || node.Finalized)
}

// SerializableOverlap reports whether two relevant nodes of one epoch were
// concurrent.
func SerializableOverlap(a, b *SerializableTx) bool {
	// Preparation orders dangerous structures but does not end overlap. A
	// snapshot taken while an entry is prepared still overlaps its later commit.
	return serializableRelevant(a) && serializableRelevant(b) && a != b && a.Ref.Epoch() == b.Ref.Epoch() && (!a.Committed || a.CommitOrder > b.SnapshotOrder) && (!b.Committed || b.CommitOrder > a.SnapshotOrder)
}

// REL_18_0 OnConflict_CheckForSerializationFailure. Finalized nodes are immune;
// prepared finalization orders conflicts without resolving committed outcome.
func (reg *Registry) SerializableEdgeLocked(current, reader, writer *SerializableTx) error {
	if !SerializableOverlap(reader, writer) || reader.Doomed || writer.Doomed {
		return nil
	}
	if _, exists := reader.Outgoing[writer]; exists {
		return nil
	}
	failure := false
	for out := range writer.Outgoing {
		if !serializableRelevant(out) || out.Doomed {
			continue
		}
		if out.Finalized && (!reader.Finalized || out.Order <= reader.Order) && (!writer.Finalized || out.Order <= writer.Order) && (!serializableReadOnly(reader) || out.Order <= reader.SnapshotOrder) {
			failure = true
			break
		}
	}
	if !failure && writer.Finalized && !serializableReadOnly(reader) {
		for in := range reader.Incoming {
			if serializableRelevant(in) && !in.Doomed && (!in.Finalized || in.Order >= writer.Order) && (!serializableReadOnly(in) || in.SnapshotOrder >= writer.Order) {
				failure = true
				break
			}
		}
	}
	if failure {
		if current == writer {
			writer.Doomed = true
			return serializableError("Canceled on identification as a pivot, during write.")
		}
		if writer.Finalized {
			return serializableError(fmt.Sprintf("Canceled on conflict out to pivot %d, during read.", writer.AssignedXID))
		}
		writer.Doomed = true
	}
	reader.Outgoing[writer] = struct{}{}
	writer.Incoming[reader] = struct{}{}
	return nil
}

// SerializablePrecommitLocked fails a doomed or pivot-closing commit and dooms
// peer pivots that this commit would complete. Caller holds Engine.mu.
func (reg *Registry) SerializablePrecommitLocked(ref ids.TxRef) error {
	node := reg.Serializable.Nodes[ref]
	if !serializableRelevant(node) {
		return nil
	}
	if node.Doomed {
		return serializableError("Canceled on identification as a pivot, during commit attempt.")
	}
	if reg.Serializable.order == ^uint64(0) {
		return errors.New("stuntgres: serialization finalization order exhausted")
	}
	if node.Finalized {
		return nil
	}
	for pivot := range node.Incoming {
		if !serializableRelevant(pivot) || !pivot.Prepared || pivot.Doomed {
			continue
		}
		for in := range pivot.Incoming {
			if in == node || serializableRelevant(in) && !in.Finalized && !serializableReadOnly(in) && !in.Doomed {
				return serializableError("Canceled on commit attempt with conflict in from prepared pivot.")
			}
		}
	}
	// Validate all potential vetoes before selecting any peer victim.
	for pivot := range node.Incoming {
		if !serializableRelevant(pivot) || pivot.Finalized || pivot.Doomed {
			continue
		}
		for in := range pivot.Incoming {
			if in == node || serializableRelevant(in) && !in.Finalized && !serializableReadOnly(in) && !in.Doomed {
				pivot.Doomed = true
				break
			}
		}
	}
	return nil
}

// Called only at the callback-free installation boundary, Engine.mu held.
func (reg *Registry) SerializableCommittedLocked(ref ids.TxRef, frontier ids.CommitSeq) {
	node := reg.Serializable.Nodes[ref]
	if !serializableRelevant(node) {
		return
	}
	reg.Serializable.order++
	node.CommitOrder = reg.Serializable.order
	if !node.Finalized {
		node.Order = node.CommitOrder
		node.Finalized = true
	}
	node.CommitFrontier = frontier
	node.Committed = true
	reg.resolveSerializableCandidatesLocked(node)
}

// SerializablePreparedLocked finalizes ref's order at PREPARE TRANSACTION.
// Caller holds Engine.mu.
func (reg *Registry) SerializablePreparedLocked(ref ids.TxRef) {
	node := reg.Serializable.Nodes[ref]
	if !serializableRelevant(node) {
		return
	}
	reg.Serializable.order++
	node.Order = reg.Serializable.order
	node.Finalized, node.Prepared = true, true
}

func (reg *Registry) resolveSerializableCandidatesLocked(writer *SerializableTx) {
	for _, candidate := range reg.Serializable.Nodes {
		if candidate.Resolved {
			continue
		}
		if _, possible := candidate.Possible[writer]; !possible {
			continue
		}
		delete(candidate.Possible, writer)
		if writer.Committed && writer.DidWrite {
			for out := range writer.Outgoing {
				if serializableRelevant(out) && out.Committed && out.Order <= candidate.SnapshotOrder {
					candidate.Unsafe = true
					break
				}
			}
		}
		if candidate.Unsafe || len(candidate.Possible) == 0 {
			candidate.Resolved = true
			if !candidate.Unsafe {
				candidate.Exempt = true
			}
			if candidate.Ready != nil {
				close(candidate.Ready)
			}
		}
	}
}

// EndTransactionLocked completes ref's identity, removes its visibility
// entry and ends its tracker node, waking a safe-snapshot waiter. The graph
// is cleared once no live participant remains. Caller holds Engine.mu.
func (reg *Registry) EndTransactionLocked(ref ids.TxRef, committed bool) {
	reg.EndTransactionIdentityLocked(ref, committed)
	delete(reg.Entries, ref)
	node := reg.Serializable.Nodes[ref]
	if node == nil || node.Ended {
		return
	}
	node.Ended = true
	if !committed {
		node.Aborted = true
		reg.resolveSerializableCandidatesLocked(node)
	}
	if node.Ready != nil && !node.Resolved {
		node.Resolved = true
		close(node.Ready)
	}
	for _, active := range reg.Serializable.Nodes {
		if !active.Ended && !active.Exempt && !active.Aborted {
			return
		}
	}
	clear(reg.Serializable.Nodes)
	clear(reg.Serializable.Creators)
}

// FenceLocked drops every visibility entry and aborts every tracker node,
// waking unresolved acquisitions. Caller holds Engine.mu.
func (reg *Registry) FenceLocked() {
	clear(reg.Entries)
	for _, node := range reg.Serializable.Nodes {
		node.Aborted = true
		if node.Ready != nil && !node.Resolved {
			node.Resolved = true
			close(node.Ready)
		}
	}
	clear(reg.Serializable.Nodes)
	clear(reg.Serializable.Creators)
}
