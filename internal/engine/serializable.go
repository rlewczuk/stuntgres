package engine

import (
	"errors"
	"fmt"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
)

// Engine.mu owns the dependency graph, candidates and final publication order.
// No graph operation takes a peer transaction's mutex. Records outlive their
// session while overlapping owners remain; visibility roots stay TX-owned.
type serializableTracker struct {
	Nodes    map[ids.TxRef]*serializableTransaction
	Creators map[ids.VersionID]ids.TxRef
	order    uint64
}

type serializableTransaction struct {
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
	Writes                                              map[ids.ObjectAddress]map[serializableWriteKey]serializableWrite
	Incoming, Outgoing                                  map[*serializableTransaction]struct{}
	Possible                                            map[*serializableTransaction]struct{}
	Ready                                               chan struct{}
	Resolved, Unsafe                                    bool
}

// A retained event contains identities, never row values. baseBorn and creator
// select the invisible creator rather than an ender of that invisible version.
type serializableWrite struct {
	Row               ids.RowID
	Version, Base     ids.VersionID
	BaseBorn          ids.CommitSeq
	Creator           ids.TxRef
	Deletion, Rewrite bool
}

// Creation and ending of the same version survive/reconcile independently.
// The zero version is reserved for the relation-wide rewrite event.
type serializableWriteKey struct {
	Version  ids.VersionID
	Deletion bool
}

func stableIsolation(level Isolation) bool { return level == RepeatableRead || level == Serializable }

func serializableError(detail string) error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "40001", Message: "could not serialize access due to read/write dependencies among transactions", Detail: "Reason code: " + detail, Hint: "The transaction might succeed if retried."}
}

// enrollSerializableLocked fences ref against the current catalog epoch and
// applies its isolation to the tracker. Caller holds Engine.mu.
func (e *Engine) enrollSerializableLocked(ref ids.TxRef, isolation Isolation) {
	if ref.ID() == 0 || ref.Epoch() != e.catalog.state.Epoch() {
		return
	}
	e.visibility.EnrollSerializableLocked(ref, isolation == Serializable)
}

// enrollSerializableLocked creates ref's tracker node for a serializable
// transaction and drops an uncaptured one otherwise. A captured node keeps
// its snapshot's mode. Caller holds Engine.mu and has fenced ref.
func (reg *visibilityRegistry) EnrollSerializableLocked(ref ids.TxRef, serializable bool) {
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
			reg.Serializable.Nodes = make(map[ids.TxRef]*serializableTransaction)
		}
		if reg.Serializable.Creators == nil {
			reg.Serializable.Creators = make(map[ids.VersionID]ids.TxRef)
		}
		reg.Serializable.Nodes[ref] = &serializableTransaction{Ref: ref, AssignedXID: reg.Entries[ref].AssignedXID, Reads: make(map[ids.ObjectAddress]struct{}), Writes: make(map[ids.ObjectAddress]map[serializableWriteKey]serializableWrite), Incoming: make(map[*serializableTransaction]struct{}), Outgoing: make(map[*serializableTransaction]struct{})}
	}
}

func serializableRelevant(node *serializableTransaction) bool {
	return node != nil && node.Captured && !node.Aborted && !node.Exempt
}
func serializableReadOnly(node *serializableTransaction) bool {
	return !node.DidWrite && (node.ReadOnly || node.Finalized)
}
func serializableOverlap(a, b *serializableTransaction) bool {
	// Preparation orders dangerous structures but does not end overlap. A
	// snapshot taken while an entry is prepared still overlaps its later commit.
	return serializableRelevant(a) && serializableRelevant(b) && a != b && a.Ref.Epoch() == b.Ref.Epoch() && (!a.Committed || a.CommitOrder > b.SnapshotOrder) && (!b.Committed || b.CommitOrder > a.SnapshotOrder)
}

// REL_18_0 OnConflict_CheckForSerializationFailure. Finalized nodes are immune;
// prepared finalization orders conflicts without resolving committed outcome.
func (reg *visibilityRegistry) SerializableEdgeLocked(current, reader, writer *serializableTransaction) error {
	if !serializableOverlap(reader, writer) || reader.Doomed || writer.Doomed {
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

func (reg *visibilityRegistry) SerializablePrecommitLocked(ref ids.TxRef) error {
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
func (reg *visibilityRegistry) SerializableCommittedLocked(ref ids.TxRef, frontier ids.CommitSeq) {
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

func (reg *visibilityRegistry) SerializablePreparedLocked(ref ids.TxRef) {
	node := reg.Serializable.Nodes[ref]
	if !serializableRelevant(node) {
		return
	}
	reg.Serializable.order++
	node.Order = reg.Serializable.order
	node.Finalized, node.Prepared = true, true
}

func (reg *visibilityRegistry) resolveSerializableCandidatesLocked(writer *serializableTransaction) {
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

func (e *Engine) endSerializable(ref ids.TxRef, committed bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.endSerializableLocked(ref, committed)
}

// endSerializableLocked ends ref in the registry and drops its index build
// waits. Caller holds Engine.mu.
func (e *Engine) endSerializableLocked(ref ids.TxRef, committed bool) {
	e.visibility.EndTransactionLocked(ref, committed)
	for index, wait := range e.indexBuildWaits {
		if wait.owner == ref {
			delete(e.indexBuildWaits, index)
		}
	}
}

// endTransactionLocked completes ref's identity, removes its visibility
// entry and ends its tracker node, waking a safe-snapshot waiter. The graph
// is cleared once no live participant remains. Caller holds Engine.mu.
func (reg *visibilityRegistry) EndTransactionLocked(ref ids.TxRef, committed bool) {
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

// Epoch replacement wakes every unresolved acquisition. Operation/ref epoch
// validation supplies the ordinary fence error, not a serialization diagnostic.
func (e *Engine) fenceSerializableLocked() {
	e.visibility.FenceLocked()
	clear(e.indexBuildWaits)
}

// fenceLocked drops every visibility entry and aborts every tracker node,
// waking unresolved acquisitions. Caller holds Engine.mu.
func (reg *visibilityRegistry) FenceLocked() {
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
