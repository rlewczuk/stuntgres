package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func preparedUnitControl(t *testing.T, e *core.Engine, s *session.Session, kind parser.TransactionControlKind, gid string) error {
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
	e := engine.New()
	origin, peer := sessionStart(t, e, nil), sessionStart(t, e, nil)
	op := transactionOperation(t, origin)
	ref, err := e.Begin(op, session.TxOptions{})
	op.Done()
	if err != nil {
		t.Fatal(err)
	}
	ticket := origin.Transactions.Ticket
	if err := preparedUnitControl(t, e, origin, parser.KindPrepareTransaction, "move"); err != nil {
		t.Fatal(err)
	}
	entry := core.EnginePreparedRef(e).Lookup("move")
	if entry == nil || entry.Ticket != ticket || entry.Ref != ref || origin.Transactions.Ticket != nil || origin.TransactionState() != session.TxIdle {
		t.Fatal("ownership was not transferred once")
	}
	if err := origin.Transactions.Validate(ref); err == nil {
		t.Fatal("detached reference still authorized")
	}
	entry.Claimed = true
	err = preparedUnitControl(t, e, peer, parser.KindRollbackPrepared, "move")
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "55000" || core.EnginePreparedRef(e).Lookup("move") != entry {
		t.Fatal(err, "claim consumed resources")
	}
	entry.Claimed = false
	ctx, cancel := context.WithCancel(context.Background())
	canceled, err := peer.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	err = core.EngineCompletePrepared(e, canceled, "move", true)
	canceled.Done()
	if !errors.Is(err, context.Canceled) || entry.Claimed || core.EnginePreparedRef(e).Lookup("move") != entry || e.Locks.TransactionTicket(ref) != ticket {
		t.Fatal(err, "cancellation consumed entry or ticket")
	}
	origin.RequestEnd(session.CleanClose)
	if err := origin.WaitEnd(context.Background()); err != nil {
		t.Fatal(err)
	}
	if core.EnginePreparedRef(e).Lookup("move") != entry || entry.Ticket != ticket {
		t.Fatal("origin exit consumed registry")
	}
	if e.Locks.TransactionTicket(ref) != ticket {
		t.Fatal("physical origin exit released prepared lock journal")
	}
	if err := preparedUnitControl(t, e, peer, parser.KindCommitPrepared, "move"); err != nil {
		t.Fatal(err)
	}
	if core.EnginePreparedRef(e).Len() != 0 {
		t.Fatal("completed entry retained")
	}
	if e.Locks.TransactionTicket(ref) != nil {
		t.Fatal("completion retained original lock journal")
	}
}

func TestPreparedTransactionFailureEndsStickyFootprint(t *testing.T) {
	e := engine.New()
	s := sessionStart(t, e, nil)
	op := transactionOperation(t, s)
	_, err := e.Begin(op, session.TxOptions{})
	op.Done()
	if err != nil {
		t.Fatal(err)
	}
	s.Transactions.AccessedTemporary = true
	err = preparedUnitControl(t, e, s, parser.KindPrepareTransaction, "temp")
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" || s.TransactionState() != session.TxIdle || s.Transactions.AccessedTemporary || s.Transactions.Ticket != nil || core.EnginePreparedRef(e).Len() != 0 {
		t.Fatal(err, "failed prepare did not dispose entire owner")
	}
}

func TestPreparedSerializableOutcomeAndPivot(t *testing.T) {
	e := engine.New()
	in := serializableUnitNode(t, e, false)
	pivot := serializableUnitNode(t, e, false)
	out := serializableUnitNode(t, e, false)
	e.Mu.Lock()
	defer e.Mu.Unlock()
	if err := e.Visibility.SerializableEdgeLocked(in, in, pivot); err != nil {
		t.Fatal(err)
	}
	if err := e.Visibility.SerializableEdgeLocked(pivot, pivot, out); err != nil {
		t.Fatal(err)
	}
	e.Visibility.SerializablePreparedLocked(pivot.Ref)
	if pivot.Committed || pivot.Ended || !pivot.Finalized || !pivot.Prepared {
		t.Fatal("prepare resolved actual outcome")
	}
	serializableUnitDiagnostic(t, e.Visibility.SerializablePrecommitLocked(out.Ref), "Canceled on commit attempt with conflict in from prepared pivot.")
	if pivot.Doomed {
		t.Fatal("prepared pivot selected as victim")
	}
	order := pivot.Order
	e.Visibility.SerializableCommittedLocked(pivot.Ref, e.Visibility.CommitSeq())
	if !pivot.Committed || pivot.Order != order {
		t.Fatal("completion reordered finalized transaction")
	}
}

func TestPreparedSerializableOverlapUsesActualOutcome(t *testing.T) {
	e := engine.New()
	writer := serializableUnitNode(t, e, false)
	e.Mu.Lock()
	writer.DidWrite = true
	e.Visibility.SerializablePreparedLocked(writer.Ref)
	prepareOrder := writer.Order
	e.Mu.Unlock()
	reader := serializableUnitNode(t, e, false)
	e.Mu.Lock()
	if !visibility.SerializableOverlap(writer, reader) {
		e.Mu.Unlock()
		t.Fatal("snapshot acquired during prepared interval lost overlap")
	}
	e.Visibility.SerializableCommittedLocked(writer.Ref, e.Visibility.CommitSeq())
	if writer.Order != prepareOrder || writer.CommitOrder <= reader.SnapshotOrder || !visibility.SerializableOverlap(writer, reader) {
		e.Mu.Unlock()
		t.Fatal("actual completion erased prepared-interval overlap or prepare order")
	}
	e.Mu.Unlock()
	later := serializableUnitNode(t, e, false)
	e.Mu.Lock()
	defer e.Mu.Unlock()
	if visibility.SerializableOverlap(writer, later) {
		t.Fatal("postcommit snapshot still overlapped finished writer")
	}
}
