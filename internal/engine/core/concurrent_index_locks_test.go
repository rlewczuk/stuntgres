package core_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqlstate"
)

func concurrentUnitRequestWait(t *testing.T, request *lock.Request) error {
	t.Helper()
	select {
	case <-request.Done():
		return request.Wait()
	case <-time.After(5 * time.Second):
		t.Fatal("completion request did not terminate")
		return nil
	}
}

func TestConcurrentIndexU2OrdinaryShareThenWriterQueue(t *testing.T) {
	e := engine.New()
	a, b, c, d := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, a, "CREATE TABLE t(n INT)")
	enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(1)")
	enginetest.SessionSQL(t, a, "BEGIN ISOLATION LEVEL READ COMMITTED")
	enginetest.SessionSQL(t, a, "LOCK TABLE t IN ROW EXCLUSIVE MODE")
	ref := enginetest.RoleTableRef(t, e, "t")
	_, build := indexMaintenanceAsync(t, e, b, "CREATE INDEX ix ON t(n)")
	enginetest.LockPending(t, e, 1)
	_, insert := indexMaintenanceAsync(t, e, c, "INSERT INTO t VALUES(3)")
	enginetest.LockPending(t, e, 2)
	lockState := e.Locks.Inspect()
	entry := lockState.Resources[lock.Resource{ObjectRef: ref}]
	exact := len(entry.Pending) == 2 && entry.Pending[0].Mode == lock.Share && entry.Pending[0].Ticket.Ref().Session() == b.SessionToken && entry.Pending[1].Mode == lock.RowExclusive && entry.Pending[1].Ticket.Ref().Session() == c.SessionToken && entry.Holders[a.Transactions.Ticket].Mode&lock.RowExclusive != 0
	if !exact {
		t.Fatal("ordinary Share build did not precede and block later RX request")
	}
	e.Mu.Lock()
	namespace := e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "public"})
	_, exists := e.Catalog.State.LookupName(ids.NameKey{ClassID: ids.RelationClassID, Namespace: namespace, Name: "ix"})
	e.Mu.Unlock()
	if exists {
		t.Fatal("ordinary blocked build published a descriptor")
	}
	enginetest.SessionSQL(t, d, "BEGIN ISOLATION LEVEL READ COMMITTED")
	rows := enginetest.SessionSQL(t, d, "SELECT n FROM t ORDER BY n")
	rows.Rows.Close()
	lockState = e.Locks.Inspect()
	entry = lockState.Resources[lock.Resource{ObjectRef: ref}]
	compatible := entry.Holders[d.Transactions.Ticket].Mode&lock.AccessShare != 0 && len(entry.Pending) == 2
	if !compatible {
		t.Fatal("compatible reader did not obtain real grant across Share/RX queue")
	}
	enginetest.SessionSQL(t, d, "COMMIT")
	enginetest.SessionSQL(t, a, "COMMIT")
	if err := build(); err != nil {
		t.Fatal(err)
	}
	if err := insert(); err != nil {
		t.Fatal(err)
	}
	index, root := concurrentUnitIndex(t, e, "ix", true, true)
	if !reflect.DeepEqual(btreeKeys(t, root), []string{"1", "3"}) {
		t.Fatal("queued ordinary consumers lost effects")
	}
	concurrentUnitRootMatchesStorage(t, e, index)
	enginetest.LockEmpty(t, e)
}

func TestConcurrentIndexU2CapturedWriterCohortAndTopLevelOutcome(t *testing.T) {
	e := engine.New()
	a, b, c := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, a, "CREATE TABLE t(n INT)")
	enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(1)")
	enginetest.SessionSQL(t, a, "BEGIN ISOLATION LEVEL READ COMMITTED")
	enginetest.SessionSQL(t, a, "LOCK TABLE t IN ROW EXCLUSIVE MODE")
	ref := enginetest.RoleTableRef(t, e, "t")
	owner := a.Transactions.Ticket
	_, await := indexMaintenanceAsync(t, e, b, "CREATE INDEX CONCURRENTLY ix ON t(n)")
	wait := concurrentUnitCompletion(t, e, owner, b)
	lockState := e.Locks.Inspect()
	parentEntry := lockState.Resources[lock.Resource{ObjectRef: ref}]
	actual := parentEntry.Holders[wait.Ticket()].Mode&lock.ShareUpdateExclusive != 0 && parentEntry.Holders[wait.Ticket()].Mode&lock.Share == 0 && len(parentEntry.Pending) == 0
	if !actual {
		t.Fatal("concurrent builder used a queued parent Share acquisition")
	}
	cohort, err := e.Locks.IndexWriters(wait.Ticket(), ref)
	if err != nil || len(cohort) != 1 || cohort[0] != owner {
		t.Fatal(cohort, err)
	}
	enginetest.SessionSQL(t, c, "BEGIN ISOLATION LEVEL READ COMMITTED")
	enginetest.SessionSQL(t, c, "LOCK TABLE t IN ROW EXCLUSIVE MODE")
	if len(cohort) != 1 || cohort[0] != owner {
		t.Fatal("later writer mutated captured cohort")
	}
	// Removing all relation contributions must not discharge completion ownership.
	e.Locks.Restore(owner, nil)
	lockState = e.Locks.Inspect()
	stillWaiting := !wait.Terminal() && lockState.Resources[wait.Resource()].Holders[owner].Mode == lock.TransactionCompletion
	if !stillWaiting {
		t.Fatal("savepoint/contribution release impersonated transaction outcome")
	}
	enginetest.SessionSQL(t, a, "ROLLBACK")
	// The second cohort, unlike the captured first one, sees this later LOCK owner.
	concurrentUnitCompletion(t, e, c.Transactions.Ticket, b)
	concurrentUnitIndex(t, e, "ix", true, false)
	enginetest.SessionSQL(t, c, "COMMIT")
	if err := await(); err != nil {
		t.Fatal(err)
	}
	index, _ := concurrentUnitIndex(t, e, "ix", true, true)
	concurrentUnitRootMatchesStorage(t, e, index)
	enginetest.LockEmpty(t, e)
}

func TestConcurrentIndexU2CompletionHardMixedDeadlock(t *testing.T) {
	e := engine.New()
	a, b := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, a, "CREATE TABLE t(n INT)")
	ta, tb := enginetest.TransactionBegin(t, a), enginetest.TransactionBegin(t, b)
	ref := enginetest.RoleTableRef(t, e, "t")
	if err := e.Locks.Acquire(context.Background(), a.Transactions.Ticket, lock.Resource{ObjectRef: ref}, lock.RowExclusive); err != nil {
		t.Fatal(err)
	}
	if err := e.Locks.Acquire(context.Background(), b.Transactions.Ticket, lock.Resource{ObjectRef: ref}, lock.ShareUpdateExclusive); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	completion, err := e.Locks.StartAcquire(ctx, b.Transactions.Ticket, lock.CompletionOf(a.Transactions.Ticket), lock.TransactionCompletion, lock.Policy{CompletionPurpose: lock.CompletionVirtual})
	if err != nil || completion == nil {
		t.Fatal(completion, err)
	}
	exclusive, err := e.Locks.StartAcquire(ctx, a.Transactions.Ticket, lock.Resource{ObjectRef: ref}, lock.AccessExclusive, lock.Policy{})
	if err != nil || exclusive == nil {
		t.Fatal(exclusive, err)
	}
	err = concurrentUnitRequestWait(t, exclusive)
	var diagnostic *sqlstate.Error
	first := fmt.Sprintf("Transaction %d (session %d, epoch %d) waits for AccessExclusiveLock on relation %d (class %d, sub-ID 0, epoch %d); blocked by transaction %d (session %d, epoch %d).", ta.ID(), ta.Session().ID(), ta.Epoch(), ref.Address.ObjectID, ref.Address.ClassID, ref.Epoch, tb.ID(), tb.Session().ID(), tb.Epoch())
	second := fmt.Sprintf("Transaction %d (session %d, epoch %d) waits for transaction completion; blocked by transaction %d (session %d, epoch %d).", tb.ID(), tb.Session().ID(), tb.Epoch(), ta.ID(), ta.Session().ID(), ta.Epoch())
	if !errors.As(err, &diagnostic) {
		t.Fatal(err)
	}
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "40P01", Message: "deadlock detected", Hint: "See server log for query details.", Detail: diagnostic.Detail}
	if !reflect.DeepEqual(*diagnostic, want) || (diagnostic.Detail != first+"\n"+second && diagnostic.Detail != second+"\n"+first) {
		t.Fatal(diagnostic)
	}
	lockState := e.Locks.Inspect()
	hardSurvives := !completion.Terminal() && lockState.Resources[completion.Resource()].Holders[a.Transactions.Ticket].Mode == lock.TransactionCompletion
	if !hardSurvives {
		t.Fatal("hard completion edge was removed or reordered as a soft relation queue")
	}
	enginetest.TransactionEnd(t, a, ta, false)
	if err := concurrentUnitRequestWait(t, completion); err != nil {
		t.Fatal(err)
	}
	enginetest.TransactionEnd(t, b, tb, false)
	enginetest.LockEmpty(t, e)
}

func TestConcurrentIndexU6CompletionTerminalArbitration(t *testing.T) {
	for _, mode := range []string{"cancel", "statement_deadline", "lock_deadline", "grant", "epoch"} {
		t.Run(mode, func(t *testing.T) {
			e := engine.New()
			a, b := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
			ta, tb := enginetest.TransactionBegin(t, a), enginetest.TransactionBegin(t, b)
			owner, waiter := a.Transactions.Ticket, b.Transactions.Ticket
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			policy := lock.Policy{CompletionPurpose: lock.CompletionVirtual}
			if mode == "lock_deadline" {
				policy.Timeout = time.Nanosecond
			}
			if mode == "statement_deadline" {
				policy.StatementDeadline = time.Now().Add(-time.Second)
			}
			request, err := e.Locks.StartAcquire(ctx, waiter, lock.CompletionOf(owner), lock.TransactionCompletion, policy)
			if mode == "statement_deadline" {
				var diagnostic *sqlstate.Error
				if request != nil || !errors.As(err, &diagnostic) || diagnostic.Code != "57014" {
					t.Fatal(request, err)
				}
			} else {
				if request == nil || err != nil {
					t.Fatal(request, err)
				}
				switch mode {
				case "cancel":
					cancel()
					if err := concurrentUnitRequestWait(t, request); !errors.Is(err, context.Canceled) {
						t.Fatal(err)
					}
				case "lock_deadline":
					var diagnostic *sqlstate.Error
					if err := concurrentUnitRequestWait(t, request); !errors.As(err, &diagnostic) || diagnostic.Code != "55P03" {
						t.Fatal(err)
					}
				case "grant":
					enginetest.TransactionEnd(t, a, ta, false)
					if err := concurrentUnitRequestWait(t, request); err != nil {
						t.Fatal(err)
					}
					cancel()
					// An ended exact owner is immediately complete; no new virtual authority.
					next, err := e.Locks.StartAcquire(context.Background(), waiter, lock.CompletionOf(owner), lock.TransactionCompletion, lock.Policy{CompletionPurpose: lock.CompletionVirtual})
					if next != nil || err != nil {
						t.Fatal(next, err)
					}
					lockState := e.Locks.Inspect(waiter)
					_, retained := lockState.Journals[waiter][lock.CompletionOf(owner)]
					terminal := request.Terminal() && request.Err() == nil
					if retained || !terminal {
						t.Fatal("late callback retained or retracted successful completion")
					}
				case "epoch":
					if err := engine.Reset(context.Background(), e); err != nil {
						t.Fatal(err)
					}
					if err := concurrentUnitRequestWait(t, request); !errors.Is(err, ids.ErrCatalogReplaced) {
						t.Fatal(err)
					}
				}
			}
			if a.TransactionState() != session.TxIdle {
				enginetest.TransactionEnd(t, a, ta, false)
			}
			enginetest.TransactionEnd(t, b, tb, false)
			enginetest.LockEmpty(t, e)
			e.Mu.Lock()
			registry := len(e.Visibility.Entries)
			e.Mu.Unlock()
			if registry != 0 {
				t.Fatal("terminal visibility registry leak")
			}
		})
	}
}

func btreeKeys(t *testing.T, root *storage.IndexRoot) []string {
	t.Helper()
	keys := make([]string, len(root.Entries))
	identities := make(map[ids.VersionID]bool)
	for i, entry := range root.Entries {
		if entry.Row == 0 || entry.Version == 0 || identities[entry.Version] {
			t.Fatal("collapsed/invalid identity", entry)
		}
		identities[entry.Version] = true
		value := entry.Keys[0]
		if value.IsNull() {
			keys[i] = "NULL"
		} else {
			var err error
			keys[i], err = value.Type().Output(context.Background(), value)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	return keys
}
