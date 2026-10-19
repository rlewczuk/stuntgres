package visibility

import (
	"fmt"
	"sync"
	"testing"
)

func TestSerializableGraphDirectionDedupAndLoneEdge(t *testing.T) {
	f := newFixture(t)
	a := f.node(false)
	b := f.node(false)
	f.mu.Lock()
	defer f.mu.Unlock()
	for range 2 {
		if err := f.reg.SerializableEdgeLocked(a, a, b); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.reg.SerializableEdgeLocked(a, a, a); err != nil {
		t.Fatal(err)
	}
	if len(a.Outgoing) != 1 || len(a.Incoming) != 0 || len(b.Incoming) != 1 || len(b.Outgoing) != 0 {
		t.Fatal("wrong direction/dedup/self edge")
	}
	if err := f.reg.SerializablePrecommitLocked(a.Ref); err != nil {
		t.Fatal(err)
	}
	f.reg.SerializableCommittedLocked(a.Ref, f.reg.CommitSeq())
	if err := f.reg.SerializablePrecommitLocked(b.Ref); err != nil {
		t.Fatal(err)
	}
	f.reg.SerializableCommittedLocked(b.Ref, f.reg.CommitSeq())
	if !a.Committed || !b.Committed || a.Doomed || b.Doomed || a.Order >= b.Order {
		t.Fatal("lone edge/empty commit order")
	}
}
func TestSerializableGraphCommitOrderingAndReadOnlyRefinement(t *testing.T) {
	for _, readOnly := range []bool{false, true} {
		for _, outBeforeSnapshot := range []bool{false, true} {
			t.Run(fmt.Sprintf("ro%t/before%t", readOnly, outBeforeSnapshot), func(t *testing.T) {
				f := newFixture(t)
				in := f.node(readOnly)
				pivot := f.node(false)
				out := f.node(false)
				f.mu.Lock()
				defer f.mu.Unlock()
				pivot.Outgoing[out] = struct{}{}
				out.Incoming[pivot] = struct{}{}
				f.reg.SerializableCommittedLocked(out.Ref, f.reg.CommitSeq())
				if outBeforeSnapshot {
					in.SnapshotOrder = out.Order
				}
				err := f.reg.SerializableEdgeLocked(pivot, in, pivot)
				if readOnly && !outBeforeSnapshot {
					if err != nil || pivot.Doomed {
						t.Fatal("late Tout falsely aborted read-only Tin", err)
					}
				} else {
					serializableUnitDiagnostic(t, err, "Canceled on identification as a pivot, during write.")
				}
			})
		}
	}
}
func TestSerializableGraphFinalizedPivotIdentity(t *testing.T) {
	f := newFixture(t)
	reader := f.node(false)
	pivot := f.node(false)
	out := f.node(false)
	if reader.Ref.ID() != pivot.Ref.ID() || reader.Ref == pivot.Ref || reader.Ref.Diagnostic() == pivot.Ref.Diagnostic() || pivot.Ref.Diagnostic() == 0 {
		t.Fatal("qualified/diagnostic identities conflated sessions")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	pivot.Outgoing[out] = struct{}{}
	out.Incoming[pivot] = struct{}{}
	if err := f.reg.AssignTransactionXIDLocked(pivot.Ref); err != nil {
		t.Fatal(err)
	}
	publicXID := f.reg.Entries[pivot.Ref].AssignedXID
	f.reg.RecordCommitTimestampLocked(pivot.Ref)
	if f.reg.Entries[pivot.Ref].AssignedXID != 0 || pivot.AssignedXID != publicXID {
		t.Fatal("completion lost the pivot's canonical diagnostic identity")
	}
	f.reg.SerializableCommittedLocked(out.Ref, f.reg.CommitSeq())
	f.reg.SerializableCommittedLocked(pivot.Ref, f.reg.CommitSeq())
	serializableUnitDiagnostic(t, f.reg.SerializableEdgeLocked(reader, reader, pivot), fmt.Sprintf("Canceled on conflict out to pivot %d, during read.", publicXID))
	if pivot.Doomed || out.Doomed || !pivot.Committed {
		t.Fatal("finalized victim mutated")
	}
}
func TestSerializableGraphNonDangerousOrderAndExcludedCauses(t *testing.T) {
	for _, excluded := range []string{"earlier_reader", "aborted", "doomed", "self"} {
		t.Run(excluded, func(t *testing.T) {
			f := newFixture(t)
			in := f.node(false)
			pivot := f.node(false)
			out := f.node(false)
			f.mu.Lock()
			defer f.mu.Unlock()
			if excluded == "earlier_reader" {
				f.reg.SerializableCommittedLocked(in.Ref, f.reg.CommitSeq())
			}
			pivot.Outgoing[out] = struct{}{}
			out.Incoming[pivot] = struct{}{}
			f.reg.SerializableCommittedLocked(out.Ref, f.reg.CommitSeq())
			if excluded == "aborted" {
				out.Aborted = true
			}
			if excluded == "doomed" {
				out.Doomed = true
			}
			if excluded == "self" {
				in = pivot
			}
			if err := f.reg.SerializableEdgeLocked(pivot, in, pivot); err != nil || pivot.Doomed {
				t.Fatal("excluded/non-dangerous cause", err)
			}
		})
	}
}
func TestSerializableGraphConcurrentFinalPreflights(t *testing.T) {
	f := newFixture(t)
	a := f.node(false)
	b := f.node(false)
	f.mu.Lock()
	f.reg.SerializableEdgeLocked(a, a, b)
	f.reg.SerializableEdgeLocked(b, b, a)
	f.mu.Unlock()
	start := make(chan struct{})
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for _, node := range []*SerializableTx{a, b} {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			f.mu.Lock()
			err := f.reg.SerializablePrecommitLocked(node.Ref)
			if err == nil {
				f.reg.SerializableCommittedLocked(node.Ref, f.reg.CommitSeq())
			}
			f.mu.Unlock()
			results <- err
		}()
	}
	close(start)
	workers.Wait()
	close(results)
	ok, failed := 0, 0
	for err := range results {
		if err == nil {
			ok++
		} else {
			failed++
			serializableUnitDiagnostic(t, err, "Canceled on identification as a pivot, during commit attempt.")
		}
	}
	if ok != 1 || failed != 1 {
		t.Fatal("atomic preflight permitted invalid pair", ok, failed)
	}
}
func TestSerializableGraphCohortRetentionAndDrain(t *testing.T) {
	f := newFixture(t)
	a := f.node(false)
	b := f.node(false)
	f.mu.Lock()
	f.reg.SerializableEdgeLocked(a, a, b)
	f.reg.SerializableCommittedLocked(a.Ref, f.reg.CommitSeq())
	f.reg.EndTransactionLocked(a.Ref, true)
	if f.reg.Serializable.Nodes[a.Ref] != a || len(b.Incoming) != 1 {
		t.Fatal("commit end erased overlapping evidence")
	}
	f.reg.EndTransactionLocked(b.Ref, false)
	f.reg.EndTransactionLocked(b.Ref, false)
	if len(f.reg.Serializable.Nodes) != 0 || len(f.reg.Serializable.Creators) != 0 {
		t.Fatal("cohort did not drain idempotently")
	}
	f.mu.Unlock()
	c := f.node(false)
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.reg.SerializablePrecommitLocked(c.Ref); err != nil {
		t.Fatal("new cohort inherited conflicts", err)
	}
}

func TestSerializablePrecommitRetainsAttemptedWriteReadOnlyHistory(t *testing.T) {
	f := newFixture(t)
	in := f.node(true)
	pivot := f.node(false)
	out := f.node(false)
	f.mu.Lock()
	defer f.mu.Unlock()
	// Child rollback has discarded private writes and current SQL mode is RO,
	// but an attempted participating write still makes Tin an implicit writer.
	in.DidWrite = true
	in.Outgoing[pivot] = struct{}{}
	pivot.Incoming[in] = struct{}{}
	pivot.Outgoing[out] = struct{}{}
	out.Incoming[pivot] = struct{}{}
	if err := f.reg.SerializablePrecommitLocked(out.Ref); err != nil {
		t.Fatal(err)
	}
	if !pivot.Doomed || in.Doomed || out.Doomed {
		t.Fatal("read-only assignment erased attempted-write history")
	}
}
