package engine

import (
	"context"
	"testing"
	"time"
)

func concurrentUnitSnapshotScope(t *testing.T, e *Engine, s *Session) (*StatementScope, *Operation, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	op, err := s.Acquire(ctx)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	scope, err := e.StartStatement(op)
	if err != nil {
		cancel()
		op.Done()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		if !scope.finished {
			scope.Finish(context.Canceled)
		}
		op.Done()
	})
	if err := scope.gate(statement(t, "SELECT 1")); err != nil {
		t.Fatal(err)
	}
	return scope, op, cancel
}

func concurrentUnitOlderWait(t *testing.T, scope *StatementScope, xmin uint64, cancel context.CancelFunc) func() error {
	t.Helper()
	done := make(chan error, 1)
	joined := false
	go func() { done <- scope.waitOlderIndexSnapshots(xmin, ObjectAddress{}) }()
	t.Cleanup(func() {
		cancel()
		if !joined {
			select {
			case <-done:
			case <-time.After(6 * time.Second):
				t.Error("snapshot worker did not terminate during cleanup")
			}
		}
	})
	return func() error {
		t.Helper()
		select {
		case err := <-done:
			joined = true
			return err
		case <-time.After(6 * time.Second):
			t.Fatal("snapshot worker did not terminate")
			return nil
		}
	}
}

func TestConcurrentIndexU3SnapshotLifetimeAndGlobalXID(t *testing.T) {
	e := New()
	writer, reader, rc := sessionStart(t, e, nil), sessionStart(t, e, nil), sessionStart(t, e, nil)
	sessionSQL(t, writer, "CREATE TABLE t(n INT)")
	sessionSQL(t, writer, "INSERT INTO t VALUES(1)")
	e.mu.Lock()
	counter, frontier := e.visibilityXID, e.commitSeq
	e.mu.Unlock()
	sessionSQL(t, writer, "BEGIN ISOLATION LEVEL READ COMMITTED")
	sessionSQL(t, reader, "BEGIN ISOLATION LEVEL REPEATABLE READ")
	sessionSQL(t, rc, "BEGIN ISOLATION LEVEL READ COMMITTED")
	e.mu.Lock()
	bare := e.visibilityXID == counter
	for ref, v := range e.visibility {
		bare = bare && v.xid == 0 && v.xmin == 0 && v.assignedXID == 0 && !v.snapshot && !v.safeBuilder && v.ticket != nil && v.ticket.ref == ref
	}
	e.mu.Unlock()
	if !bare {
		t.Fatal("BEGIN fabricated visibility XID or advertised snapshot")
	}
	stable := sessionSQL(t, reader, "SELECT n FROM t ORDER BY n")
	stable.Rows.Close()
	transient := sessionSQL(t, rc, "SELECT n FROM t ORDER BY n")
	e.mu.Lock()
	v, held := e.visibility[reader.transactions.ref]
	rcState, rcAssociated := e.visibility[rc.transactions.ref]
	rcHeld := rcState.snapshot || rcState.xmin != 0 || rcState.xid != 0 || rcState.assignedXID != 0 || rcState.safeBuilder
	e.mu.Unlock()
	if !held || !v.snapshot || v.xid != 0 || v.xmin != counter+1 || rcHeld || !rcAssociated || rcState.ticket != rc.transactions.ticket {
		t.Fatal("stable/RC eager result snapshot ownership", v, rcHeld)
	}
	// An uncommitted real write advances global XID, not the committed row frontier.
	sessionSQL(t, writer, "INSERT INTO t VALUES(2)")
	e.mu.Lock()
	w := e.visibility[writer.transactions.ref]
	sameFrontier := e.commitSeq == frontier
	advanced := w.xid > counter
	e.mu.Unlock()
	if !sameFrontier || !advanced || w.snapshot {
		t.Fatal("write/global-XID/RC-frontier separation", w)
	}
	other := New()
	other.mu.Lock()
	_, foreignErr := other.visibilitySnapshotLocked(reader.transactions.ref, false)
	other.mu.Unlock()
	if foreignErr == nil {
		t.Fatal("other engine accepted snapshot owner")
	}
	transient.Rows.Close()
	sessionSQL(t, writer, "ROLLBACK")
	sessionSQL(t, reader, "ROLLBACK")
	sessionSQL(t, rc, "ROLLBACK")
	e.mu.Lock()
	remaining := len(e.visibility)
	after := e.visibilityXID
	e.mu.Unlock()
	if remaining != 0 || after <= counter {
		t.Fatal("rollback leaked registry or rewound global XID")
	}
	lockEmpty(t, e)
}

func TestConcurrentIndexU3LateEndangeredEqualFrontier(t *testing.T) {
	e := New()
	writer, builder, late, bare := sessionStart(t, e, nil), sessionStart(t, e, nil), sessionStart(t, e, nil), sessionStart(t, e, nil)
	sessionSQL(t, writer, "CREATE TABLE t(n INT)")
	sessionSQL(t, writer, "CREATE TABLE u(n INT)")
	sessionSQL(t, writer, "INSERT INTO u VALUES(1)")
	sessionSQL(t, writer, "BEGIN ISOLATION LEVEL READ COMMITTED")
	sessionSQL(t, writer, "INSERT INTO t VALUES(2)")
	sessionSQL(t, bare, "BEGIN ISOLATION LEVEL REPEATABLE READ")
	scope, op, cancel := concurrentUnitSnapshotScope(t, e, builder)
	e.mu.Lock()
	frontier := e.commitSeq
	if err := e.visibilityWriteLocked(scope.txRef); err != nil {
		e.mu.Unlock()
		t.Fatal(err)
	}
	boundary, err := e.visibilitySnapshotLocked(scope.txRef, true)
	builderVisibility := e.visibility[scope.txRef]
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	// This unrelated reader's snapshot is acquired AFTER the saved validation
	// boundary but remains endangered because the older global write XID is live.
	sessionSQL(t, late, "BEGIN ISOLATION LEVEL REPEATABLE READ")
	rows := sessionSQL(t, late, "SELECT n FROM u")
	rows.Rows.Close()
	e.mu.Lock()
	snapshot := e.visibility[late.transactions.ref]
	equalFrontier := e.commitSeq == frontier
	e.mu.Unlock()
	if !snapshot.snapshot || snapshot.xmin > boundary || !equalFrontier {
		t.Fatal("late equal-frontier snapshot was not actually endangered", snapshot, boundary)
	}
	await := concurrentUnitOlderWait(t, scope, boundary, cancel)
	concurrentUnitCompletion(t, e, late.transactions.ticket, builder)
	e.mu.Lock()
	self, associated := e.visibility[scope.txRef]
	mirroredXID := scope.ticket.assignedPublicXID()
	e.mu.Unlock()
	// Phase reset withdraws all ordering/snapshot advertisement, not the
	// registered ticket's canonical assignment association.
	wantSelf := transactionVisibility{assignedXID: builderVisibility.assignedXID, ticket: scope.ticket}
	if !associated || builderVisibility.ticket != scope.ticket || self != wantSelf || mirroredXID != wantSelf.assignedXID {
		t.Fatal("builder retained phase advertisement or lost canonical ticket association during older wait", self, wantSelf, mirroredXID)
	}
	sessionSQL(t, writer, "ROLLBACK")
	e.locks.mu.Lock()
	entry := e.locks.entries[lockResource{completion: late.transactions.ticket}]
	pending := entry != nil && len(entry.pending) == 1
	e.locks.mu.Unlock()
	if !pending {
		t.Fatal("ending XID owner released another transaction's persistent snapshot")
	}
	sessionSQL(t, late, "ROLLBACK")
	if err := await(); err != nil {
		t.Fatal(err)
	}
	if err := scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	op.Done()
	sessionSQL(t, bare, "ROLLBACK")
	lockEmpty(t, e)
}

func TestConcurrentIndexU3GenuinelyNewerXminNotCommitSeq(t *testing.T) {
	e := New()
	writer, builder, newer := sessionStart(t, e, nil), sessionStart(t, e, nil), sessionStart(t, e, nil)
	sessionSQL(t, writer, "CREATE TABLE t(n INT)")
	sessionSQL(t, writer, "INSERT INTO t VALUES(1)")
	sessionSQL(t, writer, "BEGIN ISOLATION LEVEL READ COMMITTED")
	sessionSQL(t, writer, "INSERT INTO t VALUES(2)")
	e.mu.Lock()
	boundary := e.visibility[writer.transactions.ref].xid
	frontier := e.commitSeq
	e.mu.Unlock()
	sessionSQL(t, writer, "ROLLBACK")
	sessionSQL(t, newer, "BEGIN ISOLATION LEVEL REPEATABLE READ")
	rows := sessionSQL(t, newer, "SELECT n FROM t")
	rows.Rows.Close()
	e.mu.Lock()
	v := e.visibility[newer.transactions.ref]
	equalFrontier := frontier == e.commitSeq
	e.mu.Unlock()
	if v.xmin <= boundary || !equalFrontier {
		t.Fatal("newer xmin incorrectly tied to CommitSeq", v, boundary)
	}
	scope, op, _ := concurrentUnitSnapshotScope(t, e, builder)
	if err := scope.waitOlderIndexSnapshots(boundary, ObjectAddress{}); err != nil {
		t.Fatal(err)
	}
	lockPending(t, e, 0)
	if err := scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	op.Done()
	sessionSQL(t, newer, "ROLLBACK")
	lockEmpty(t, e)
}

func TestConcurrentIndexU3SafeBuilderClassification(t *testing.T) {
	for _, tc := range []struct {
		name, key string
		safe      bool
	}{{"simple", "n", true}, {"parenthesized", "(n)", true}, {"collated", `(v COLLATE "C")`, true}, {"expression", "(n+1)", false}, {"coercion", "(n::BIGINT)", false}} {
		t.Run(tc.name, func(t *testing.T) {
			e := New()
			owner, builder := sessionStart(t, e, nil), sessionStart(t, e, nil)
			sessionSQL(t, owner, "CREATE TABLE t(n INT,v TEXT)")
			sessionSQL(t, owner, "CREATE INDEX ix ON t("+tc.key+")")
			definition, _ := concurrentUnitIndex(t, e, "ix", true, true)
			parent := e.CatalogRead().state.descriptors[definition.sqlIndex.parent].(*relationDescriptor)
			safe, err := simpleConcurrentIndex(parent, definition.sqlIndex)
			if err != nil || safe != tc.safe {
				t.Fatal("catalog key classification differs from safe-builder policy", safe, err)
			}
			sessionSQL(t, owner, "BEGIN ISOLATION LEVEL READ COMMITTED")
			scope, op, cancel := concurrentUnitSnapshotScope(t, e, builder)
			e.mu.Lock()
			xmin, err := e.visibilitySnapshotLocked(owner.transactions.ref, safe)
			e.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			await := concurrentUnitOlderWait(t, scope, xmin, cancel)
			if !tc.safe {
				concurrentUnitCompletion(t, e, owner.transactions.ticket, builder)
				sessionSQL(t, owner, "ROLLBACK")
			}
			if err := await(); err != nil {
				t.Fatal(err)
			}
			if err := scope.Finish(nil); err != nil {
				t.Fatal(err)
			}
			op.Done()
			if tc.safe {
				sessionSQL(t, owner, "ROLLBACK")
			}
			lockEmpty(t, e)
		})
	}
}

func TestConcurrentIndexU3ResetClearsRegistryNotCounter(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE TABLE t(n INT)")
	sessionSQL(t, s, "BEGIN ISOLATION LEVEL REPEATABLE READ")
	rows := sessionSQL(t, s, "SELECT n FROM t")
	rows.Rows.Close()
	e.mu.Lock()
	counter := e.visibilityXID
	had := len(e.visibility) > 0
	e.mu.Unlock()
	if !had {
		t.Fatal("no real stable snapshot before reset")
	}
	if err := e.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	empty := len(e.visibility) == 0
	unchanged := e.visibilityXID == counter
	e.mu.Unlock()
	if !empty || !unchanged {
		t.Fatal("epoch replacement retained owners or rewound global chronology")
	}
	sessionSQL(t, s, "ROLLBACK")
	lockEmpty(t, e)
}
