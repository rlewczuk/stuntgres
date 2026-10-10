package engine

import (
	"errors"
	"fmt"

	"stuntgres/internal/sqlstate"
)

// Engine.mu owns the dependency graph, candidates and final publication order.
// No graph operation takes a peer transaction's mutex. Records outlive their
// session while overlapping owners remain; visibility roots stay TX-owned.
type serializableTracker struct {
	nodes    map[TxRef]*serializableTransaction
	creators map[VersionID]TxRef
	order    uint64
}

type serializableTransaction struct {
	ref                                                 TxRef
	captured, readOnly, deferrable                      bool
	frontier                                            CommitSeq
	snapshotOrder, order                                uint64
	commitFrontier                                      CommitSeq
	commitOrder                                         uint64
	didWrite, doomed, committed, ended, aborted, exempt bool
	finalized, prepared                                 bool
	reads                                               map[ObjectAddress]struct{}
	writes                                              map[ObjectAddress]map[serializableWriteKey]serializableWrite
	incoming, outgoing                                  map[*serializableTransaction]struct{}
	possible                                            map[*serializableTransaction]struct{}
	ready                                               chan struct{}
	resolved, unsafe                                    bool
}

// A retained event contains identities, never row values. baseBorn and creator
// select the invisible creator rather than an ender of that invisible version.
type serializableWrite struct {
	row               RowID
	version, base     VersionID
	baseBorn          CommitSeq
	creator           TxRef
	deletion, rewrite bool
}

// Creation and ending of the same version survive/reconcile independently.
// The zero version is reserved for the relation-wide rewrite event.
type serializableWriteKey struct {
	version  VersionID
	deletion bool
}

func stableIsolation(level Isolation) bool { return level == RepeatableRead || level == Serializable }

func serializableError(detail string) error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "40001", Message: "could not serialize access due to read/write dependencies among transactions", Detail: "Reason code: " + detail, Hint: "The transaction might succeed if retried."}
}

func (e *Engine) enrollSerializableLocked(ref TxRef, isolation Isolation) {
	if ref.id == 0 || ref.epoch != e.state.epoch {
		return
	}
	node := e.serializable.nodes[ref]
	if node != nil && node.captured {
		return
	}
	if isolation != Serializable {
		delete(e.serializable.nodes, ref)
		return
	}
	if node == nil {
		if e.serializable.nodes == nil {
			e.serializable.nodes = make(map[TxRef]*serializableTransaction)
		}
		if e.serializable.creators == nil {
			e.serializable.creators = make(map[VersionID]TxRef)
		}
		e.serializable.nodes[ref] = &serializableTransaction{ref: ref, reads: make(map[ObjectAddress]struct{}), writes: make(map[ObjectAddress]map[serializableWriteKey]serializableWrite), incoming: make(map[*serializableTransaction]struct{}), outgoing: make(map[*serializableTransaction]struct{})}
	}
}

func serializableRelevant(node *serializableTransaction) bool {
	return node != nil && node.captured && !node.aborted && !node.exempt
}
func serializableReadOnly(node *serializableTransaction) bool {
	return !node.didWrite && (node.readOnly || node.finalized)
}
func serializableOverlap(a, b *serializableTransaction) bool {
	// Preparation orders dangerous structures but does not end overlap. A
	// snapshot taken while an entry is prepared still overlaps its later commit.
	return serializableRelevant(a) && serializableRelevant(b) && a != b && a.ref.epoch == b.ref.epoch && (!a.committed || a.commitOrder > b.snapshotOrder) && (!b.committed || b.commitOrder > a.snapshotOrder)
}

// REL_18_0 OnConflict_CheckForSerializationFailure. Finalized nodes are immune;
// prepared finalization orders conflicts without resolving committed outcome.
func (e *Engine) serializableEdgeLocked(current, reader, writer *serializableTransaction) error {
	if !serializableOverlap(reader, writer) || reader.doomed || writer.doomed {
		return nil
	}
	if _, exists := reader.outgoing[writer]; exists {
		return nil
	}
	failure := false
	for out := range writer.outgoing {
		if !serializableRelevant(out) || out.doomed {
			continue
		}
		if out.finalized && (!reader.finalized || out.order <= reader.order) && (!writer.finalized || out.order <= writer.order) && (!serializableReadOnly(reader) || out.order <= reader.snapshotOrder) {
			failure = true
			break
		}
	}
	if !failure && writer.finalized && !serializableReadOnly(reader) {
		for in := range reader.incoming {
			if serializableRelevant(in) && !in.doomed && (!in.finalized || in.order >= writer.order) && (!serializableReadOnly(in) || in.snapshotOrder >= writer.order) {
				failure = true
				break
			}
		}
	}
	if failure {
		if current == writer {
			writer.doomed = true
			return serializableError("Canceled on identification as a pivot, during write.")
		}
		if writer.finalized {
			return serializableError(fmt.Sprintf("Canceled on conflict out to pivot %d, during read.", writer.ref.diagnostic))
		}
		writer.doomed = true
	}
	reader.outgoing[writer] = struct{}{}
	writer.incoming[reader] = struct{}{}
	return nil
}

func (e *Engine) serializablePrecommitLocked(ref TxRef) error {
	node := e.serializable.nodes[ref]
	if !serializableRelevant(node) {
		return nil
	}
	if node.doomed {
		return serializableError("Canceled on identification as a pivot, during commit attempt.")
	}
	if e.serializable.order == ^uint64(0) {
		return errors.New("stuntgres: serialization finalization order exhausted")
	}
	if node.finalized {
		return nil
	}
	for pivot := range node.incoming {
		if !serializableRelevant(pivot) || !pivot.prepared || pivot.doomed {
			continue
		}
		for in := range pivot.incoming {
			if in == node || serializableRelevant(in) && !in.finalized && !serializableReadOnly(in) && !in.doomed {
				return serializableError("Canceled on commit attempt with conflict in from prepared pivot.")
			}
		}
	}
	// Validate all potential vetoes before selecting any peer victim.
	for pivot := range node.incoming {
		if !serializableRelevant(pivot) || pivot.finalized || pivot.doomed {
			continue
		}
		for in := range pivot.incoming {
			if in == node || serializableRelevant(in) && !in.finalized && !serializableReadOnly(in) && !in.doomed {
				pivot.doomed = true
				break
			}
		}
	}
	return nil
}

// Called only at the callback-free installation boundary, Engine.mu held.
func (e *Engine) serializableCommittedLocked(ref TxRef, frontier CommitSeq) {
	node := e.serializable.nodes[ref]
	if !serializableRelevant(node) {
		return
	}
	e.serializable.order++
	node.commitOrder = e.serializable.order
	if !node.finalized {
		node.order = node.commitOrder
		node.finalized = true
	}
	node.commitFrontier = frontier
	node.committed = true
	e.resolveSerializableCandidatesLocked(node)
}

func (e *Engine) serializablePreparedLocked(ref TxRef) {
	node := e.serializable.nodes[ref]
	if !serializableRelevant(node) {
		return
	}
	e.serializable.order++
	node.order = e.serializable.order
	node.finalized, node.prepared = true, true
}

func (e *Engine) resolveSerializableCandidatesLocked(writer *serializableTransaction) {
	for _, candidate := range e.serializable.nodes {
		if candidate.resolved {
			continue
		}
		if _, possible := candidate.possible[writer]; !possible {
			continue
		}
		delete(candidate.possible, writer)
		if writer.committed && writer.didWrite {
			for out := range writer.outgoing {
				if serializableRelevant(out) && out.committed && out.order <= candidate.snapshotOrder {
					candidate.unsafe = true
					break
				}
			}
		}
		if candidate.unsafe || len(candidate.possible) == 0 {
			candidate.resolved = true
			if !candidate.unsafe {
				candidate.exempt = true
			}
			if candidate.ready != nil {
				close(candidate.ready)
			}
		}
	}
}

func (e *Engine) endSerializable(ref TxRef, committed bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.endSerializableLocked(ref, committed)
}
func (e *Engine) endSerializableLocked(ref TxRef, committed bool) {
	delete(e.visibility, ref)
	for index, wait := range e.indexBuildWaits {
		if wait.owner == ref {
			delete(e.indexBuildWaits, index)
		}
	}
	node := e.serializable.nodes[ref]
	if node == nil || node.ended {
		return
	}
	node.ended = true
	if !committed {
		node.aborted = true
		e.resolveSerializableCandidatesLocked(node)
	}
	if node.ready != nil && !node.resolved {
		node.resolved = true
		close(node.ready)
	}
	for _, active := range e.serializable.nodes {
		if !active.ended && !active.exempt && !active.aborted {
			return
		}
	}
	clear(e.serializable.nodes)
	clear(e.serializable.creators)
}

// Epoch replacement wakes every unresolved acquisition. Operation/ref epoch
// validation supplies the ordinary fence error, not a serialization diagnostic.
func (e *Engine) fenceSerializableLocked() {
	clear(e.visibility)
	clear(e.indexBuildWaits)
	for _, node := range e.serializable.nodes {
		node.aborted = true
		if node.ready != nil && !node.resolved {
			node.resolved = true
			close(node.ready)
		}
	}
	clear(e.serializable.nodes)
	clear(e.serializable.creators)
}
