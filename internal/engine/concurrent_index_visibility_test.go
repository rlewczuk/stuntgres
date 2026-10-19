package engine

import (
	"context"
	"testing"
	"time"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
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
	go func() { done <- scope.waitOlderIndexSnapshots(xmin, ids.ObjectAddress{}) }()
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
	counter, frontier := e.visibility.VisibilityXID(), e.visibility.CommitSeq()
	e.mu.Unlock()
	sessionSQL(t, writer, "BEGIN ISOLATION LEVEL READ COMMITTED")
	sessionSQL(t, reader, "BEGIN ISOLATION LEVEL REPEATABLE READ")
	sessionSQL(t, rc, "BEGIN ISOLATION LEVEL READ COMMITTED")
	e.mu.Lock()
	bare := e.visibility.VisibilityXID() == counter
	for ref, v := range e.visibility.Entries {
		bare = bare && v.XID == 0 && v.Xmin == 0 && v.AssignedXID == 0 && !v.Snapshot && !v.SafeBuilder && v.Ticket != nil && v.Ticket.Ref() == ref
	}
	e.mu.Unlock()
	if !bare {
		t.Fatal("BEGIN fabricated visibility XID or advertised snapshot")
	}
	stable := sessionSQL(t, reader, "SELECT n FROM t ORDER BY n")
	stable.Rows.Close()
	transient := sessionSQL(t, rc, "SELECT n FROM t ORDER BY n")
	e.mu.Lock()
	v, held := e.visibility.Entries[reader.transactions.ref]
	rcState, rcAssociated := e.visibility.Entries[rc.transactions.ref]
	rcHeld := rcState.Snapshot || rcState.Xmin != 0 || rcState.XID != 0 || rcState.AssignedXID != 0 || rcState.SafeBuilder
	e.mu.Unlock()
	if !held || !v.Snapshot || v.XID != 0 || v.Xmin != counter+1 || rcHeld || !rcAssociated || rcState.Ticket != rc.transactions.ticket {
		t.Fatal("stable/RC eager result snapshot ownership", v, rcHeld)
	}
	// An uncommitted real write advances global XID, not the committed row frontier.
	sessionSQL(t, writer, "INSERT INTO t VALUES(2)")
	e.mu.Lock()
	w := e.visibility.Entries[writer.transactions.ref]
	sameFrontier := e.visibility.CommitSeq() == frontier
	advanced := w.XID > counter
	e.mu.Unlock()
	if !sameFrontier || !advanced || w.Snapshot {
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
	remaining := len(e.visibility.Entries)
	after := e.visibility.VisibilityXID()
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
	frontier := e.visibility.CommitSeq()
	if err := e.visibilityWriteLocked(scope.txRef); err != nil {
		e.mu.Unlock()
		t.Fatal(err)
	}
	boundary, err := e.visibilitySnapshotLocked(scope.txRef, true)
	builderVisibility := e.visibility.Entries[scope.txRef]
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
	snapshot := e.visibility.Entries[late.transactions.ref]
	equalFrontier := e.visibility.CommitSeq() == frontier
	e.mu.Unlock()
	if !snapshot.Snapshot || snapshot.Xmin > boundary || !equalFrontier {
		t.Fatal("late equal-frontier snapshot was not actually endangered", snapshot, boundary)
	}
	await := concurrentUnitOlderWait(t, scope, boundary, cancel)
	concurrentUnitCompletion(t, e, late.transactions.ticket, builder)
	e.mu.Lock()
	self, associated := e.visibility.Entries[scope.txRef]
	mirroredXID := scope.ticket.AssignedPublicXID()
	e.mu.Unlock()
	// Phase reset withdraws all ordering/snapshot advertisement, not the
	// registered ticket's canonical assignment association.
	wantSelf := transactionVisibility{AssignedXID: builderVisibility.AssignedXID, FullXID: builderVisibility.FullXID, Ticket: scope.ticket}
	if !associated || builderVisibility.Ticket != scope.ticket || self != wantSelf || mirroredXID != wantSelf.AssignedXID {
		t.Fatal("builder retained phase advertisement or lost canonical ticket association during older wait", self, wantSelf, mirroredXID)
	}
	sessionSQL(t, writer, "ROLLBACK")
	lockState := e.locks.Inspect()
	entry := lockState.Resources[lock.CompletionOf(late.transactions.ticket)]
	pending := entry != nil && len(entry.Pending) == 1
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
	boundary := e.visibility.Entries[writer.transactions.ref].XID
	frontier := e.visibility.CommitSeq()
	e.mu.Unlock()
	sessionSQL(t, writer, "ROLLBACK")
	sessionSQL(t, newer, "BEGIN ISOLATION LEVEL REPEATABLE READ")
	rows := sessionSQL(t, newer, "SELECT n FROM t")
	rows.Rows.Close()
	e.mu.Lock()
	v := e.visibility.Entries[newer.transactions.ref]
	equalFrontier := frontier == e.visibility.CommitSeq()
	e.mu.Unlock()
	if v.Xmin <= boundary || !equalFrontier {
		t.Fatal("newer xmin incorrectly tied to CommitSeq", v, boundary)
	}
	scope, op, _ := concurrentUnitSnapshotScope(t, e, builder)
	if err := scope.waitOlderIndexSnapshots(boundary, ids.ObjectAddress{}); err != nil {
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
			parent := e.CatalogRead().MustRelation(definition.SQLIndex.Parent)
			safe, err := simpleConcurrentIndex(parent, definition.SQLIndex)
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
	counter := e.visibility.VisibilityXID()
	had := len(e.visibility.Entries) > 0
	e.mu.Unlock()
	if !had {
		t.Fatal("no real stable snapshot before reset")
	}
	if err := e.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	empty := len(e.visibility.Entries) == 0
	unchanged := e.visibility.VisibilityXID() == counter
	e.mu.Unlock()
	if !empty || !unchanged {
		t.Fatal("epoch replacement retained owners or rewound global chronology")
	}
	sessionSQL(t, s, "ROLLBACK")
	lockEmpty(t, e)
}
