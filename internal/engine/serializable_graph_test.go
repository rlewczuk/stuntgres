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
	node := e.serializable.nodes[ref]
	node.captured = true
	node.readOnly = readOnly
	node.frontier = e.commitSeq
	node.snapshotOrder = e.serializable.order
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
		if err := e.serializableEdgeLocked(a, a, b); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.serializableEdgeLocked(a, a, a); err != nil {
		t.Fatal(err)
	}
	if len(a.outgoing) != 1 || len(a.incoming) != 0 || len(b.incoming) != 1 || len(b.outgoing) != 0 {
		t.Fatal("wrong direction/dedup/self edge")
	}
	if err := e.serializablePrecommitLocked(a.ref); err != nil {
		t.Fatal(err)
	}
	e.serializableCommittedLocked(a.ref, e.commitSeq)
	if err := e.serializablePrecommitLocked(b.ref); err != nil {
		t.Fatal(err)
	}
	e.serializableCommittedLocked(b.ref, e.commitSeq)
	if !a.committed || !b.committed || a.doomed || b.doomed || a.order >= b.order {
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
			if err := e.serializableEdgeLocked(a, a, b); err != nil {
				t.Fatal(err)
			}
			if err := e.serializableEdgeLocked(b, b, a); err != nil {
				t.Fatal(err)
			}
			if err := e.serializablePrecommitLocked(first.ref); err != nil {
				t.Fatal(err)
			}
			e.serializableCommittedLocked(first.ref, e.commitSeq)
			if !last.doomed || last.committed || last.ref.attachment.state != TxActive || first.doomed {
				t.Fatal("peer doom became an asynchronous TX abort")
			}
			serializableUnitDiagnostic(t, e.serializablePrecommitLocked(last.ref), "Canceled on identification as a pivot, during commit attempt.")
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
				pivot.outgoing[out] = struct{}{}
				out.incoming[pivot] = struct{}{}
				e.serializableCommittedLocked(out.ref, e.commitSeq)
				if outBeforeSnapshot {
					in.snapshotOrder = out.order
				}
				err := e.serializableEdgeLocked(pivot, in, pivot)
				if readOnly && !outBeforeSnapshot {
					if err != nil || pivot.doomed {
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
	if reader.ref.ID() != pivot.ref.ID() || reader.ref == pivot.ref || reader.ref.diagnostic == pivot.ref.diagnostic || pivot.ref.diagnostic == 0 {
		t.Fatal("qualified/diagnostic identities conflated sessions")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	pivot.outgoing[out] = struct{}{}
	out.incoming[pivot] = struct{}{}
	if err := e.assignTransactionXIDLocked(pivot.ref); err != nil {
		t.Fatal(err)
	}
	publicXID := e.visibility[pivot.ref].assignedXID
	e.recordCommitTimestampLocked(pivot.ref)
	if e.visibility[pivot.ref].assignedXID != 0 || pivot.assignedXID != publicXID {
		t.Fatal("completion lost the pivot's canonical diagnostic identity")
	}
	e.serializableCommittedLocked(out.ref, e.commitSeq)
	e.serializableCommittedLocked(pivot.ref, e.commitSeq)
	serializableUnitDiagnostic(t, e.serializableEdgeLocked(reader, reader, pivot), fmt.Sprintf("Canceled on conflict out to pivot %d, during read.", publicXID))
	if pivot.doomed || out.doomed || !pivot.committed {
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
				e.serializableCommittedLocked(in.ref, e.commitSeq)
			}
			pivot.outgoing[out] = struct{}{}
			out.incoming[pivot] = struct{}{}
			e.serializableCommittedLocked(out.ref, e.commitSeq)
			if excluded == "aborted" {
				out.aborted = true
			}
			if excluded == "doomed" {
				out.doomed = true
			}
			if excluded == "self" {
				in = pivot
			}
			if err := e.serializableEdgeLocked(pivot, in, pivot); err != nil || pivot.doomed {
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
	e.serializableEdgeLocked(a, a, b)
	e.serializableEdgeLocked(b, b, a)
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
			err := e.serializablePrecommitLocked(node.ref)
			if err == nil {
				e.serializableCommittedLocked(node.ref, e.commitSeq)
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
	e.serializableEdgeLocked(a, a, b)
	e.serializableCommittedLocked(a.ref, e.commitSeq)
	e.endSerializableLocked(a.ref, true)
	if e.serializable.nodes[a.ref] != a || len(b.incoming) != 1 {
		t.Fatal("commit end erased overlapping evidence")
	}
	e.endSerializableLocked(b.ref, false)
	e.endSerializableLocked(b.ref, false)
	if len(e.serializable.nodes) != 0 || len(e.serializable.creators) != 0 {
		t.Fatal("cohort did not drain idempotently")
	}
	e.mu.Unlock()
	c := serializableUnitNode(t, e, false)
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.serializablePrecommitLocked(c.ref); err != nil {
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
	in.didWrite = true
	in.outgoing[pivot] = struct{}{}
	pivot.incoming[in] = struct{}{}
	pivot.outgoing[out] = struct{}{}
	out.incoming[pivot] = struct{}{}
	if err := e.serializablePrecommitLocked(out.ref); err != nil {
		t.Fatal(err)
	}
	if !pivot.doomed || in.doomed || out.doomed {
		t.Fatal("read-only assignment erased attempted-write history")
	}
}
