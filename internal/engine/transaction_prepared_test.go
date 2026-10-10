package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func preparedUnitControl(t *testing.T, e *Engine, s *Session, kind parser.TransactionControlKind, gid string) error {
	t.Helper()
	op := transactionOperation(t, s)
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	_, err = scope.Execute(&parser.TransactionControl{Kind: kind, GID: gid}, 0, nil)
	finish := scope.Finish(err)
	if finish != err || scope.Finish(nil) != err {
		t.Fatal("control Finish replaced its execution diagnostic", err, finish)
	}
	return finish
}

func TestPreparedTransactionMoveAndClaim(t *testing.T) {
	e := New()
	origin, peer := sessionStart(t, e, nil), sessionStart(t, e, nil)
	op := transactionOperation(t, origin)
	ref, err := e.Begin(op, TxOptions{})
	op.Done()
	if err != nil {
		t.Fatal(err)
	}
	ticket := origin.transactions.ticket
	if err := preparedUnitControl(t, e, origin, parser.KindPrepareTransaction, "move"); err != nil {
		t.Fatal(err)
	}
	entry := e.prepared["move"]
	if entry == nil || entry.ticket != ticket || entry.ref != ref || origin.transactions.ticket != nil || origin.TransactionState() != TxIdle {
		t.Fatal("ownership was not transferred once")
	}
	if err := origin.transactions.validate(ref); err == nil {
		t.Fatal("detached reference still authorized")
	}
	entry.claimed = true
	err = preparedUnitControl(t, e, peer, parser.KindRollbackPrepared, "move")
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "55000" || e.prepared["move"] != entry {
		t.Fatal(err, "claim consumed resources")
	}
	entry.claimed = false
	ctx, cancel := context.WithCancel(context.Background())
	canceled, err := peer.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	err = e.completePrepared(canceled, "move", true)
	canceled.Done()
	if !errors.Is(err, context.Canceled) || entry.claimed || e.prepared["move"] != entry || e.locks.owners[ref] != ticket {
		t.Fatal(err, "cancellation consumed entry or ticket")
	}
	origin.RequestEnd(CleanClose)
	if err := origin.WaitEnd(context.Background()); err != nil {
		t.Fatal(err)
	}
	if e.prepared["move"] != entry || entry.ticket != ticket {
		t.Fatal("origin exit consumed registry")
	}
	if e.locks.owners[ref] != ticket {
		t.Fatal("physical origin exit released prepared lock journal")
	}
	if err := preparedUnitControl(t, e, peer, parser.KindCommitPrepared, "move"); err != nil {
		t.Fatal(err)
	}
	if len(e.prepared) != 0 {
		t.Fatal("completed entry retained")
	}
	if e.locks.owners[ref] != nil {
		t.Fatal("completion retained original lock journal")
	}
}

func TestPreparedTransactionFailureEndsStickyFootprint(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	op := transactionOperation(t, s)
	_, err := e.Begin(op, TxOptions{})
	op.Done()
	if err != nil {
		t.Fatal(err)
	}
	s.transactions.accessedTemporary = true
	err = preparedUnitControl(t, e, s, parser.KindPrepareTransaction, "temp")
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" || s.TransactionState() != TxIdle || s.transactions.accessedTemporary || s.transactions.ticket != nil || len(e.prepared) != 0 {
		t.Fatal(err, "failed prepare did not dispose entire owner")
	}
}

func TestPreparedSerializableOutcomeAndPivot(t *testing.T) {
	e := New()
	in := serializableUnitNode(t, e, false)
	pivot := serializableUnitNode(t, e, false)
	out := serializableUnitNode(t, e, false)
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.serializableEdgeLocked(in, in, pivot); err != nil {
		t.Fatal(err)
	}
	if err := e.serializableEdgeLocked(pivot, pivot, out); err != nil {
		t.Fatal(err)
	}
	e.serializablePreparedLocked(pivot.ref)
	if pivot.committed || pivot.ended || !pivot.finalized || !pivot.prepared {
		t.Fatal("prepare resolved actual outcome")
	}
	serializableUnitDiagnostic(t, e.serializablePrecommitLocked(out.ref), "Canceled on commit attempt with conflict in from prepared pivot.")
	if pivot.doomed {
		t.Fatal("prepared pivot selected as victim")
	}
	order := pivot.order
	e.serializableCommittedLocked(pivot.ref, e.commitSeq)
	if !pivot.committed || pivot.order != order {
		t.Fatal("completion reordered finalized transaction")
	}
}

func TestPreparedSerializableOverlapUsesActualOutcome(t *testing.T) {
	e := New()
	writer := serializableUnitNode(t, e, false)
	e.mu.Lock()
	writer.didWrite = true
	e.serializablePreparedLocked(writer.ref)
	prepareOrder := writer.order
	e.mu.Unlock()
	reader := serializableUnitNode(t, e, false)
	e.mu.Lock()
	if !serializableOverlap(writer, reader) {
		e.mu.Unlock()
		t.Fatal("snapshot acquired during prepared interval lost overlap")
	}
	e.serializableCommittedLocked(writer.ref, e.commitSeq)
	if writer.order != prepareOrder || writer.commitOrder <= reader.snapshotOrder || !serializableOverlap(writer, reader) {
		e.mu.Unlock()
		t.Fatal("actual completion erased prepared-interval overlap or prepare order")
	}
	e.mu.Unlock()
	later := serializableUnitNode(t, e, false)
	e.mu.Lock()
	defer e.mu.Unlock()
	if serializableOverlap(writer, later) {
		t.Fatal("postcommit snapshot still overlapped finished writer")
	}
}
