package indexcmds_test

import (
	"context"
	"runtime"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/indexcmds"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/engine/visibility"
)

func concurrentUnitSnapshotScope(t *testing.T, e *core.Engine, s *session.Session) (*core.StatementScope, *session.Operation, context.CancelFunc) {
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
		if !scope.Finished {
			scope.Finish(context.Canceled)
		}
		op.Done()
	})
	if err := scope.Gate(enginetest.Statement(t, "SELECT 1")); err != nil {
		t.Fatal(err)
	}
	return scope, op, cancel
}

func concurrentUnitOlderWait(t *testing.T, scope *core.StatementScope, xmin uint64, cancel context.CancelFunc) func() error {
	t.Helper()
	done := make(chan error, 1)
	joined := false
	go func() { done <- indexcmds.WaitOlderIndexSnapshots(scope, xmin, ids.ObjectAddress{}) }()
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
	e := engine.New()
	writer, reader, rc := sessionStart(t, e, nil), sessionStart(t, e, nil), sessionStart(t, e, nil)
	sessionSQL(t, writer, "CREATE TABLE t(n INT)")
	sessionSQL(t, writer, "INSERT INTO t VALUES(1)")
	e.Mu.Lock()
	counter, frontier := e.Visibility.VisibilityXID(), e.Visibility.CommitSeq()
	e.Mu.Unlock()
	sessionSQL(t, writer, "BEGIN ISOLATION LEVEL READ COMMITTED")
	sessionSQL(t, reader, "BEGIN ISOLATION LEVEL REPEATABLE READ")
	sessionSQL(t, rc, "BEGIN ISOLATION LEVEL READ COMMITTED")
	e.Mu.Lock()
	bare := e.Visibility.VisibilityXID() == counter
	for ref, v := range e.Visibility.Entries {
		bare = bare && v.XID == 0 && v.Xmin == 0 && v.AssignedXID == 0 && !v.Snapshot && !v.SafeBuilder && v.Ticket != nil && v.Ticket.Ref() == ref
	}
	e.Mu.Unlock()
	if !bare {
		t.Fatal("BEGIN fabricated visibility XID or advertised snapshot")
	}
	stable := sessionSQL(t, reader, "SELECT n FROM t ORDER BY n")
	stable.Rows.Close()
	transient := sessionSQL(t, rc, "SELECT n FROM t ORDER BY n")
	e.Mu.Lock()
	v, held := e.Visibility.Entries[reader.Transactions.Ref]
	rcState, rcAssociated := e.Visibility.Entries[rc.Transactions.Ref]
	rcHeld := rcState.Snapshot || rcState.Xmin != 0 || rcState.XID != 0 || rcState.AssignedXID != 0 || rcState.SafeBuilder
	e.Mu.Unlock()
	if !held || !v.Snapshot || v.XID != 0 || v.Xmin != counter+1 || rcHeld || !rcAssociated || rcState.Ticket != rc.Transactions.Ticket {
		t.Fatal("stable/RC eager result snapshot ownership", v, rcHeld)
	}
	// An uncommitted real write advances global XID, not the committed row frontier.
	sessionSQL(t, writer, "INSERT INTO t VALUES(2)")
	e.Mu.Lock()
	w := e.Visibility.Entries[writer.Transactions.Ref]
	sameFrontier := e.Visibility.CommitSeq() == frontier
	advanced := w.XID > counter
	e.Mu.Unlock()
	if !sameFrontier || !advanced || w.Snapshot {
		t.Fatal("write/global-XID/RC-frontier separation", w)
	}
	other := engine.New()
	other.Mu.Lock()
	_, foreignErr := other.VisibilitySnapshotLocked(reader.Transactions.Ref, false)
	other.Mu.Unlock()
	if foreignErr == nil {
		t.Fatal("other engine accepted snapshot owner")
	}
	transient.Rows.Close()
	sessionSQL(t, writer, "ROLLBACK")
	sessionSQL(t, reader, "ROLLBACK")
	sessionSQL(t, rc, "ROLLBACK")
	e.Mu.Lock()
	remaining := len(e.Visibility.Entries)
	after := e.Visibility.VisibilityXID()
	e.Mu.Unlock()
	if remaining != 0 || after <= counter {
		t.Fatal("rollback leaked registry or rewound global XID")
	}
	lockEmpty(t, e)
}

func TestConcurrentIndexU3LateEndangeredEqualFrontier(t *testing.T) {
	e := engine.New()
	writer, builder, late, bare := sessionStart(t, e, nil), sessionStart(t, e, nil), sessionStart(t, e, nil), sessionStart(t, e, nil)
	sessionSQL(t, writer, "CREATE TABLE t(n INT)")
	sessionSQL(t, writer, "CREATE TABLE u(n INT)")
	sessionSQL(t, writer, "INSERT INTO u VALUES(1)")
	sessionSQL(t, writer, "BEGIN ISOLATION LEVEL READ COMMITTED")
	sessionSQL(t, writer, "INSERT INTO t VALUES(2)")
	sessionSQL(t, bare, "BEGIN ISOLATION LEVEL REPEATABLE READ")
	scope, op, cancel := concurrentUnitSnapshotScope(t, e, builder)
	e.Mu.Lock()
	frontier := e.Visibility.CommitSeq()
	if err := e.VisibilityWriteLocked(scope.TxRef); err != nil {
		e.Mu.Unlock()
		t.Fatal(err)
	}
	boundary, err := e.VisibilitySnapshotLocked(scope.TxRef, true)
	builderVisibility := e.Visibility.Entries[scope.TxRef]
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	// This unrelated reader's snapshot is acquired AFTER the saved validation
	// boundary but remains endangered because the older global write XID is live.
	sessionSQL(t, late, "BEGIN ISOLATION LEVEL REPEATABLE READ")
	rows := sessionSQL(t, late, "SELECT n FROM u")
	rows.Rows.Close()
	e.Mu.Lock()
	snapshot := e.Visibility.Entries[late.Transactions.Ref]
	equalFrontier := e.Visibility.CommitSeq() == frontier
	e.Mu.Unlock()
	if !snapshot.Snapshot || snapshot.Xmin > boundary || !equalFrontier {
		t.Fatal("late equal-frontier snapshot was not actually endangered", snapshot, boundary)
	}
	await := concurrentUnitOlderWait(t, scope, boundary, cancel)
	concurrentUnitCompletion(t, e, late.Transactions.Ticket, builder)
	e.Mu.Lock()
	self, associated := e.Visibility.Entries[scope.TxRef]
	mirroredXID := scope.LockTicket.AssignedPublicXID()
	e.Mu.Unlock()
	// Phase reset withdraws all ordering/snapshot advertisement, not the
	// registered ticket's canonical assignment association.
	wantSelf := visibility.Entry{AssignedXID: builderVisibility.AssignedXID, FullXID: builderVisibility.FullXID, Ticket: scope.LockTicket}
	if !associated || builderVisibility.Ticket != scope.LockTicket || self != wantSelf || mirroredXID != wantSelf.AssignedXID {
		t.Fatal("builder retained phase advertisement or lost canonical ticket association during older wait", self, wantSelf, mirroredXID)
	}
	sessionSQL(t, writer, "ROLLBACK")
	lockState := e.Locks.Inspect()
	entry := lockState.Resources[lock.CompletionOf(late.Transactions.Ticket)]
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
	e := engine.New()
	writer, builder, newer := sessionStart(t, e, nil), sessionStart(t, e, nil), sessionStart(t, e, nil)
	sessionSQL(t, writer, "CREATE TABLE t(n INT)")
	sessionSQL(t, writer, "INSERT INTO t VALUES(1)")
	sessionSQL(t, writer, "BEGIN ISOLATION LEVEL READ COMMITTED")
	sessionSQL(t, writer, "INSERT INTO t VALUES(2)")
	e.Mu.Lock()
	boundary := e.Visibility.Entries[writer.Transactions.Ref].XID
	frontier := e.Visibility.CommitSeq()
	e.Mu.Unlock()
	sessionSQL(t, writer, "ROLLBACK")
	sessionSQL(t, newer, "BEGIN ISOLATION LEVEL REPEATABLE READ")
	rows := sessionSQL(t, newer, "SELECT n FROM t")
	rows.Rows.Close()
	e.Mu.Lock()
	v := e.Visibility.Entries[newer.Transactions.Ref]
	equalFrontier := frontier == e.Visibility.CommitSeq()
	e.Mu.Unlock()
	if v.Xmin <= boundary || !equalFrontier {
		t.Fatal("newer xmin incorrectly tied to CommitSeq", v, boundary)
	}
	scope, op, _ := concurrentUnitSnapshotScope(t, e, builder)
	if err := indexcmds.WaitOlderIndexSnapshots(scope, boundary, ids.ObjectAddress{}); err != nil {
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
			e := engine.New()
			owner, builder := sessionStart(t, e, nil), sessionStart(t, e, nil)
			sessionSQL(t, owner, "CREATE TABLE t(n INT,v TEXT)")
			sessionSQL(t, owner, "CREATE INDEX ix ON t("+tc.key+")")
			definition, _ := concurrentUnitIndex(t, e, "ix", true, true)
			parent := e.CatalogRead().MustRelation(definition.SQLIndex.Parent)
			safe, err := index.SimpleConcurrentIndex(parent, definition.SQLIndex)
			if err != nil || safe != tc.safe {
				t.Fatal("catalog key classification differs from safe-builder policy", safe, err)
			}
			sessionSQL(t, owner, "BEGIN ISOLATION LEVEL READ COMMITTED")
			scope, op, cancel := concurrentUnitSnapshotScope(t, e, builder)
			e.Mu.Lock()
			xmin, err := e.VisibilitySnapshotLocked(owner.Transactions.Ref, safe)
			e.Mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			await := concurrentUnitOlderWait(t, scope, xmin, cancel)
			if !tc.safe {
				concurrentUnitCompletion(t, e, owner.Transactions.Ticket, builder)
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
	e := engine.New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE TABLE t(n INT)")
	sessionSQL(t, s, "BEGIN ISOLATION LEVEL REPEATABLE READ")
	rows := sessionSQL(t, s, "SELECT n FROM t")
	rows.Rows.Close()
	e.Mu.Lock()
	counter := e.Visibility.VisibilityXID()
	had := len(e.Visibility.Entries) > 0
	e.Mu.Unlock()
	if !had {
		t.Fatal("no real stable snapshot before reset")
	}
	if err := engine.Reset(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	e.Mu.Lock()
	empty := len(e.Visibility.Entries) == 0
	unchanged := e.Visibility.VisibilityXID() == counter
	e.Mu.Unlock()
	if !empty || !unchanged {
		t.Fatal("epoch replacement retained owners or rewound global chronology")
	}
	sessionSQL(t, s, "ROLLBACK")
	lockEmpty(t, e)
}

func sessionStart(t *testing.T, e *core.Engine, p settings.SessionDefaultProvider) *session.Session {
	t.Helper()
	s, err := e.StartSession(context.Background(), session.SessionStart{ID: ids.SessionID(testSessionID.Add(1)), Defaults: p})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		s.RequestEnd(session.CleanClose)
		if err := s.WaitEnd(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return s
}

func sessionSQL(t *testing.T, s *session.Session, q string) core.Result {
	t.Helper()
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	r, err := core.EngineOf(s).Execute(op, enginetest.Statement(t, q), 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.Rows != nil {
		t.Cleanup(func() {
			if err := r.Rows.Close(); err != nil {
				t.Error(err)
			}
		})
	}
	return r
}

func lockEmpty(t *testing.T, e *core.Engine) {
	t.Helper()
	lockState := e.Locks.Inspect()
	if len(lockState.Owners) != 0 || len(lockState.Resources) != 0 {
		t.Fatalf("residual lock state: owners=%d entries=%d", len(lockState.Owners), len(lockState.Resources))
	}
}

func lockPending(t *testing.T, e *core.Engine, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		lockState := e.Locks.Inspect()
		got := 0
		for _, entry := range lockState.Resources {
			got += len(entry.Pending)
		}
		if got == n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("pending %d, want %d", got, n)
		}
		runtime.Gosched()
	}
}
