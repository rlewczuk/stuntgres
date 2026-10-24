package engine

import (
	"context"
	"runtime"
	"testing"
	"time"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
)

func exclusionCommitWorker(t *testing.T, s *session.Session, ref ids.TxRef) <-chan error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	done := make(chan error, 1)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		op, _, err := s.AcquireCommand(ctx)
		if err == nil {
			_, err = engineOf(s).Commit(op, ref)
			err = op.CommandError(err)
			op.Done()
		}
		done <- err
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
			t.Error("exclusion COMMIT worker did not join")
		}
	})
	return done
}

func exclusionCompletionQueued(t *testing.T, e *Engine, owner, waiter *lock.Ticket, done <-chan error) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		lockState := e.locks.Inspect()
		entry := lockState.Resources[lock.CompletionOf(owner)]
		queued := false
		if entry != nil {
			for _, request := range entry.Pending {
				if request.Ticket == waiter && request.Mode == lock.TransactionCompletion && !request.Terminal {
					queued = true
				}
			}
		}
		if queued {
			if !e.mu.TryLock() {
				t.Fatal("completion waiter retained engine metadata lock")
			}
			e.mu.Unlock()
			if !session.TxOwnerOf(waiter.Ref()).Mu.TryLock() {
				t.Fatal("completion waiter retained transaction owner lock")
			}
			session.TxOwnerOf(waiter.Ref()).Mu.Unlock()
			return
		}
		select {
		case err := <-done:
			t.Fatal("COMMIT completed before its required peer", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("missing exact exclusion completion resource edge")
		}
		runtime.Gosched()
	}
}

func exclusionCommitAwait(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("exclusion COMMIT did not finish after peer completion")
	}
}

func TestExclusionPeerCompletion(t *testing.T) {
	e := New()
	a, b := sessionStart(t, e, nil), sessionStart(t, e, nil)
	sessionSQL(t, a, "CREATE TABLE t(k INT,CONSTRAINT ex EXCLUDE USING btree(k WITH =) DEFERRABLE INITIALLY DEFERRED)")
	ar, br := transactionBegin(t, a), transactionBegin(t, b)
	sessionSQL(t, a, "INSERT INTO t VALUES(1)")
	at := a.Transactions.Ticket
	if len(ownerExtensionOrZero(a.Transactions).exclusionModes.Events) != 0 {
		t.Fatal("uncontested first admission invented a deferred obligation")
	}
	sessionSQL(t, b, "INSERT INTO t VALUES(1)")
	bt := b.Transactions.Ticket
	if len(ownerExtensionOrZero(b.Transactions).exclusionModes.Events) != 1 {
		t.Fatal("peer admission did not enlist its actual row/version")
	}
	event := ownerExtensionOrZero(b.Transactions).exclusionModes.Events[0]
	projection := e.exclusions.OwnLocked(bt)
	entries := projection.Entries[event.Index.Address]
	if len(entries) != 1 || event.Row != entries[0].Row.Row || event.Version != entries[0].Row.Version {
		t.Fatal("deferred event is not an exact admitted version", event)
	}
	done := exclusionCommitWorker(t, b, br)
	exclusionCompletionQueued(t, e, at, bt, done)
	transactionEnd(t, a, ar, false)
	exclusionCommitAwait(t, done)
	if e.exclusions.CountLocked() != 0 {
		t.Fatal("completed owners retained dirty projections")
	}
	uniqueClaimsEmpty(t, e)
}

func TestExclusionPendingLifecycle(t *testing.T) {
	e := New()
	a, b := sessionStart(t, e, nil), sessionStart(t, e, nil)
	sessionSQL(t, a, "CREATE TABLE t(k INT,CONSTRAINT ex EXCLUDE USING hash(k WITH =) DEFERRABLE INITIALLY DEFERRED)")
	transactionBegin(t, a)
	sessionSQL(t, a, "INSERT INTO t VALUES(1)")
	at := a.Transactions.Ticket
	before := e.exclusions.OwnLocked(at)
	sessionSQL(t, a, "SAVEPOINT m")
	sessionSQL(t, a, "SET CONSTRAINTS ex IMMEDIATE")
	sessionSQL(t, a, "INSERT INTO t VALUES(2)")
	if e.exclusions.OwnLocked(at) == before {
		t.Fatal("dirty projection was mutated rather than replaced")
	}
	sessionSQL(t, a, "ROLLBACK TO m")
	if e.exclusions.OwnLocked(at) != before || len(ownerExtensionOrZero(a.Transactions).exclusionModes.ByConstraint) != 0 {
		t.Fatal("savepoint did not restore projection and constraint modes")
	}
	sessionSQL(t, a, "PREPARE TRANSACTION 'exclusion_unit'")
	prepared := e.prepared.Lookup("exclusion_unit")
	if prepared == nil || prepared.Ticket != at || a.Transactions.Ticket != nil || e.exclusions.OwnLocked(at) != before {
		t.Fatal("PREPARE did not retain the detached dirty owner")
	}
	br := transactionBegin(t, b)
	sessionSQL(t, b, "INSERT INTO t VALUES(1)")
	bt := b.Transactions.Ticket
	done := exclusionCommitWorker(t, b, br)
	exclusionCompletionQueued(t, e, at, bt, done)
	sessionSQL(t, a, "ROLLBACK PREPARED 'exclusion_unit'")
	exclusionCommitAwait(t, done)
	if e.exclusions.CountLocked() != 0 || e.prepared.Len() != 0 {
		t.Fatal("prepared completion retained exclusion ownership")
	}
	uniqueClaimsEmpty(t, e)
}

func TestExclusionEagerClaims(t *testing.T) {
	for _, method := range []string{"btree", "hash"} {
		t.Run(method, func(t *testing.T) {
			e := New()
			a, b := sessionStart(t, e, nil), sessionStart(t, e, nil)
			sessionSQL(t, a, "CREATE TABLE t(k INT,CONSTRAINT ex EXCLUDE USING "+method+"(k WITH =))")
			ar := transactionBegin(t, a)
			sessionSQL(t, a, "INSERT INTO t VALUES(1)")
			done, cancel := uniqueAsyncUnit(t, b, "INSERT INTO t VALUES(1)")
			lockPending(t, e, 1)
			lockState := e.locks.Inspect()
			claimed := false
			for resource, entry := range lockState.Resources {
				if resource.Key() != nil && len(entry.Pending) == 1 && entry.Pending[0].Mode == lock.UniqueClaim {
					claimed = true
				}
			}
			if !claimed {
				t.Fatal("eager exclusion did not reuse the typed unique-key resource")
			}
			cancel()
			deleteAwait(t, done, 0, context.Canceled)
			lockPending(t, e, 0)
			transactionEnd(t, a, ar, false)
			if e.exclusions.CountLocked() != 0 {
				t.Fatal("cancelled eager admission retained dirty projection")
			}
			uniqueClaimsEmpty(t, e)
		})
	}
}
