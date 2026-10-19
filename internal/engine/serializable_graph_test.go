package engine

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"stuntgres/internal/sqlstate"
)

func serializableUnitNode(t *testing.T, e *Engine, readOnly bool) *serializableTransaction {
	t.Helper()
	s := sessionStart(t, e, nil)
	op := transactionOperation(t, s)
	ref, err := e.Begin(op, TxOptions{Isolation: Serializable, ReadOnly: readOnly})
	op.Done()
	if err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	node := e.visibility.Serializable.Nodes[ref]
	node.Captured = true
	node.ReadOnly = readOnly
	node.Frontier = e.visibility.commitSeq
	node.SnapshotOrder = e.visibility.Serializable.order
	e.mu.Unlock()
	return node
}
func serializableUnitDiagnostic(t *testing.T, err error, detail string) {
	t.Helper()
	var got *sqlstate.Error
	want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "40001", Message: "could not serialize access due to read/write dependencies among transactions", Detail: "Reason code: " + detail, Hint: "The transaction might succeed if retried."}
	if !errors.As(err, &got) || *got != *want {
		t.Fatalf("diagnostic got %#v (%v), want %#v", got, err, want)
	}
}
func TestSerializableGraphDirectionDedupAndLoneEdge(t *testing.T) {
	e := New()
	a := serializableUnitNode(t, e, false)
	b := serializableUnitNode(t, e, false)
	e.mu.Lock()
	defer e.mu.Unlock()
	for range 2 {
		if err := e.visibility.serializableEdgeLocked(a, a, b); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.visibility.serializableEdgeLocked(a, a, a); err != nil {
		t.Fatal(err)
	}
	if len(a.Outgoing) != 1 || len(a.Incoming) != 0 || len(b.Incoming) != 1 || len(b.Outgoing) != 0 {
		t.Fatal("wrong direction/dedup/self edge")
	}
	if err := e.visibility.serializablePrecommitLocked(a.Ref); err != nil {
		t.Fatal(err)
	}
	e.visibility.serializableCommittedLocked(a.Ref, e.visibility.commitSeq)
	if err := e.visibility.serializablePrecommitLocked(b.Ref); err != nil {
		t.Fatal(err)
	}
	e.visibility.serializableCommittedLocked(b.Ref, e.visibility.commitSeq)
	if !a.Committed || !b.Committed || a.Doomed || b.Doomed || a.Order >= b.Order {
		t.Fatal("lone edge/empty commit order")
	}
}
func TestSerializableGraphCycleFirstCommitDoomsActivePivot(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		t.Run(fmt.Sprint(reverse), func(t *testing.T) {
			e := New()
			a := serializableUnitNode(t, e, false)
			b := serializableUnitNode(t, e, false)
			first, last := a, b
			if reverse {
				first, last = b, a
			}
			e.mu.Lock()
			defer e.mu.Unlock()
			if err := e.visibility.serializableEdgeLocked(a, a, b); err != nil {
				t.Fatal(err)
			}
			if err := e.visibility.serializableEdgeLocked(b, b, a); err != nil {
				t.Fatal(err)
			}
			if err := e.visibility.serializablePrecommitLocked(first.Ref); err != nil {
				t.Fatal(err)
			}
			e.visibility.serializableCommittedLocked(first.Ref, e.visibility.commitSeq)
			if !last.Doomed || last.Committed || txOwnerOf(last.Ref).state != TxActive || first.Doomed {
				t.Fatal("peer doom became an asynchronous TX abort")
			}
			serializableUnitDiagnostic(t, e.visibility.serializablePrecommitLocked(last.Ref), "Canceled on identification as a pivot, during commit attempt.")
		})
	}
}
func TestSerializableGraphCommitOrderingAndReadOnlyRefinement(t *testing.T) {
	for _, readOnly := range []bool{false, true} {
		for _, outBeforeSnapshot := range []bool{false, true} {
			t.Run(fmt.Sprintf("ro%t/before%t", readOnly, outBeforeSnapshot), func(t *testing.T) {
				e := New()
				in := serializableUnitNode(t, e, readOnly)
				pivot := serializableUnitNode(t, e, false)
				out := serializableUnitNode(t, e, false)
				e.mu.Lock()
				defer e.mu.Unlock()
				pivot.Outgoing[out] = struct{}{}
				out.Incoming[pivot] = struct{}{}
				e.visibility.serializableCommittedLocked(out.Ref, e.visibility.commitSeq)
				if outBeforeSnapshot {
					in.SnapshotOrder = out.Order
				}
				err := e.visibility.serializableEdgeLocked(pivot, in, pivot)
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
	e := New()
	reader := serializableUnitNode(t, e, false)
	pivot := serializableUnitNode(t, e, false)
	out := serializableUnitNode(t, e, false)
	if reader.Ref.ID() != pivot.Ref.ID() || reader.Ref == pivot.Ref || reader.Ref.Diagnostic() == pivot.Ref.Diagnostic() || pivot.Ref.Diagnostic() == 0 {
		t.Fatal("qualified/diagnostic identities conflated sessions")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	pivot.Outgoing[out] = struct{}{}
	out.Incoming[pivot] = struct{}{}
	if err := e.assignTransactionXIDLocked(pivot.Ref); err != nil {
		t.Fatal(err)
	}
	publicXID := e.visibility.Entries[pivot.Ref].AssignedXID
	e.visibility.recordCommitTimestampLocked(pivot.Ref)
	if e.visibility.Entries[pivot.Ref].AssignedXID != 0 || pivot.AssignedXID != publicXID {
		t.Fatal("completion lost the pivot's canonical diagnostic identity")
	}
	e.visibility.serializableCommittedLocked(out.Ref, e.visibility.commitSeq)
	e.visibility.serializableCommittedLocked(pivot.Ref, e.visibility.commitSeq)
	serializableUnitDiagnostic(t, e.visibility.serializableEdgeLocked(reader, reader, pivot), fmt.Sprintf("Canceled on conflict out to pivot %d, during read.", publicXID))
	if pivot.Doomed || out.Doomed || !pivot.Committed {
		t.Fatal("finalized victim mutated")
	}
}
func TestSerializableGraphNonDangerousOrderAndExcludedCauses(t *testing.T) {
	for _, excluded := range []string{"earlier_reader", "aborted", "doomed", "self"} {
		t.Run(excluded, func(t *testing.T) {
			e := New()
			in := serializableUnitNode(t, e, false)
			pivot := serializableUnitNode(t, e, false)
			out := serializableUnitNode(t, e, false)
			e.mu.Lock()
			defer e.mu.Unlock()
			if excluded == "earlier_reader" {
				e.visibility.serializableCommittedLocked(in.Ref, e.visibility.commitSeq)
			}
			pivot.Outgoing[out] = struct{}{}
			out.Incoming[pivot] = struct{}{}
			e.visibility.serializableCommittedLocked(out.Ref, e.visibility.commitSeq)
			if excluded == "aborted" {
				out.Aborted = true
			}
			if excluded == "doomed" {
				out.Doomed = true
			}
			if excluded == "self" {
				in = pivot
			}
			if err := e.visibility.serializableEdgeLocked(pivot, in, pivot); err != nil || pivot.Doomed {
				t.Fatal("excluded/non-dangerous cause", err)
			}
		})
	}
}
func TestSerializableGraphConcurrentFinalPreflights(t *testing.T) {
	e := New()
	a := serializableUnitNode(t, e, false)
	b := serializableUnitNode(t, e, false)
	e.mu.Lock()
	e.visibility.serializableEdgeLocked(a, a, b)
	e.visibility.serializableEdgeLocked(b, b, a)
	e.mu.Unlock()
	start := make(chan struct{})
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for _, node := range []*serializableTransaction{a, b} {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			e.mu.Lock()
			err := e.visibility.serializablePrecommitLocked(node.Ref)
			if err == nil {
				e.visibility.serializableCommittedLocked(node.Ref, e.visibility.commitSeq)
			}
			e.mu.Unlock()
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
	e := New()
	a := serializableUnitNode(t, e, false)
	b := serializableUnitNode(t, e, false)
	e.mu.Lock()
	e.visibility.serializableEdgeLocked(a, a, b)
	e.visibility.serializableCommittedLocked(a.Ref, e.visibility.commitSeq)
	e.endSerializableLocked(a.Ref, true)
	if e.visibility.Serializable.Nodes[a.Ref] != a || len(b.Incoming) != 1 {
		t.Fatal("commit end erased overlapping evidence")
	}
	e.endSerializableLocked(b.Ref, false)
	e.endSerializableLocked(b.Ref, false)
	if len(e.visibility.Serializable.Nodes) != 0 || len(e.visibility.Serializable.Creators) != 0 {
		t.Fatal("cohort did not drain idempotently")
	}
	e.mu.Unlock()
	c := serializableUnitNode(t, e, false)
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.visibility.serializablePrecommitLocked(c.Ref); err != nil {
		t.Fatal("new cohort inherited conflicts", err)
	}
}

func TestSerializablePrecommitRetainsAttemptedWriteReadOnlyHistory(t *testing.T) {
	e := New()
	in := serializableUnitNode(t, e, true)
	pivot := serializableUnitNode(t, e, false)
	out := serializableUnitNode(t, e, false)
	e.mu.Lock()
	defer e.mu.Unlock()
	// Child rollback has discarded private writes and current SQL mode is RO,
	// but an attempted participating write still makes Tin an implicit writer.
	in.DidWrite = true
	in.Outgoing[pivot] = struct{}{}
	pivot.Incoming[in] = struct{}{}
	pivot.Outgoing[out] = struct{}{}
	out.Incoming[pivot] = struct{}{}
	if err := e.visibility.serializablePrecommitLocked(out.Ref); err != nil {
		t.Fatal(err)
	}
	if !pivot.Doomed || in.Doomed || out.Doomed {
		t.Fatal("read-only assignment erased attempted-write history")
	}
}
