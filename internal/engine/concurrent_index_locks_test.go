package engine

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"stuntgres/internal/sqlstate"
	"testing"
	"time"
)

func concurrentUnitRequestWait(t *testing.T, request *relationRequest) error {
	t.Helper()
	select {
	case <-request.done:
		return request.wait()
	case <-time.After(5 * time.Second):
		t.Fatal("completion request did not terminate")
		return nil
	}
}

func TestConcurrentIndexU2OrdinaryShareThenWriterQueue(t *testing.T) {
	e := New()
	a, b, c, d := sessionStart(t, e, nil), sessionStart(t, e, nil), sessionStart(t, e, nil), sessionStart(t, e, nil)
	sessionSQL(t, a, "CREATE TABLE t(n INT)")
	sessionSQL(t, a, "INSERT INTO t VALUES(1)")
	sessionSQL(t, a, "BEGIN ISOLATION LEVEL READ COMMITTED")
	sessionSQL(t, a, "LOCK TABLE t IN ROW EXCLUSIVE MODE")
	ref := roleTableRef(t, e, "t")
	_, build := indexMaintenanceAsync(t, e, b, "CREATE INDEX ix ON t(n)")
	lockPending(t, e, 1)
	_, insert := indexMaintenanceAsync(t, e, c, "INSERT INTO t VALUES(3)")
	lockPending(t, e, 2)
	e.locks.mu.Lock()
	entry := e.locks.entries[lockResource{ObjectRef: ref}]
	exact := len(entry.pending) == 2 && entry.pending[0].mode == relationShare && entry.pending[0].ticket.ref.session == b.token && entry.pending[1].mode == rowExclusive && entry.pending[1].ticket.ref.session == c.token && entry.holders[a.transactions.ticket].mode&rowExclusive != 0
	e.locks.mu.Unlock()
	if !exact {
		t.Fatal("ordinary Share build did not precede and block later RX request")
	}
	e.mu.Lock()
	namespace := e.state.names[NameKey{ClassID: NamespaceClassID, Name: "public"}]
	_, exists := e.state.names[NameKey{ClassID: RelationClassID, Namespace: namespace, Name: "ix"}]
	e.mu.Unlock()
	if exists {
		t.Fatal("ordinary blocked build published a descriptor")
	}
	sessionSQL(t, d, "BEGIN ISOLATION LEVEL READ COMMITTED")
	rows := sessionSQL(t, d, "SELECT n FROM t ORDER BY n")
	rows.Rows.Close()
	e.locks.mu.Lock()
	compatible := entry.holders[d.transactions.ticket].mode&accessShare != 0 && len(entry.pending) == 2
	e.locks.mu.Unlock()
	if !compatible {
		t.Fatal("compatible reader did not obtain real grant across Share/RX queue")
	}
	sessionSQL(t, d, "COMMIT")
	sessionSQL(t, a, "COMMIT")
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
	lockEmpty(t, e)
}

func TestConcurrentIndexU2CapturedWriterCohortAndTopLevelOutcome(t *testing.T) {
	e := New()
	a, b, c := sessionStart(t, e, nil), sessionStart(t, e, nil), sessionStart(t, e, nil)
	sessionSQL(t, a, "CREATE TABLE t(n INT)")
	sessionSQL(t, a, "INSERT INTO t VALUES(1)")
	sessionSQL(t, a, "BEGIN ISOLATION LEVEL READ COMMITTED")
	sessionSQL(t, a, "LOCK TABLE t IN ROW EXCLUSIVE MODE")
	ref := roleTableRef(t, e, "t")
	owner := a.transactions.ticket
	_, await := indexMaintenanceAsync(t, e, b, "CREATE INDEX CONCURRENTLY ix ON t(n)")
	wait := concurrentUnitCompletion(t, e, owner, b)
	e.locks.mu.Lock()
	parentEntry := e.locks.entries[lockResource{ObjectRef: ref}]
	actual := parentEntry.holders[wait.ticket].mode&shareUpdateExclusive != 0 && parentEntry.holders[wait.ticket].mode&relationShare == 0 && len(parentEntry.pending) == 0
	e.locks.mu.Unlock()
	if !actual {
		t.Fatal("concurrent builder used a queued parent Share acquisition")
	}
	cohort, err := e.locks.indexWriters(wait.ticket, ref)
	if err != nil || len(cohort) != 1 || cohort[0] != owner {
		t.Fatal(cohort, err)
	}
	sessionSQL(t, c, "BEGIN ISOLATION LEVEL READ COMMITTED")
	sessionSQL(t, c, "LOCK TABLE t IN ROW EXCLUSIVE MODE")
	if len(cohort) != 1 || cohort[0] != owner {
		t.Fatal("later writer mutated captured cohort")
	}
	// Removing all relation contributions must not discharge completion ownership.
	e.locks.restore(owner, nil)
	e.locks.mu.Lock()
	stillWaiting := !wait.terminal && wait.entry.holders[owner].mode == transactionCompletion
	e.locks.mu.Unlock()
	if !stillWaiting {
		t.Fatal("savepoint/contribution release impersonated transaction outcome")
	}
	sessionSQL(t, a, "ROLLBACK")
	// The second cohort, unlike the captured first one, sees this later LOCK owner.
	concurrentUnitCompletion(t, e, c.transactions.ticket, b)
	concurrentUnitIndex(t, e, "ix", true, false)
	sessionSQL(t, c, "COMMIT")
	if err := await(); err != nil {
		t.Fatal(err)
	}
	index, _ := concurrentUnitIndex(t, e, "ix", true, true)
	concurrentUnitRootMatchesStorage(t, e, index)
	lockEmpty(t, e)
}

func TestConcurrentIndexU2CompletionHardMixedDeadlock(t *testing.T) {
	e := New()
	a, b := sessionStart(t, e, nil), sessionStart(t, e, nil)
	sessionSQL(t, a, "CREATE TABLE t(n INT)")
	ta, tb := transactionBegin(t, a), transactionBegin(t, b)
	ref := roleTableRef(t, e, "t")
	if err := e.locks.acquire(context.Background(), a.transactions.ticket, lockResource{ObjectRef: ref}, rowExclusive); err != nil {
		t.Fatal(err)
	}
	if err := e.locks.acquire(context.Background(), b.transactions.ticket, lockResource{ObjectRef: ref}, shareUpdateExclusive); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	completion, err := e.locks.startAcquire(ctx, b.transactions.ticket, lockResource{completion: a.transactions.ticket}, transactionCompletion, relationLockPolicy{})
	if err != nil || completion == nil {
		t.Fatal(completion, err)
	}
	exclusive, err := e.locks.startAcquire(ctx, a.transactions.ticket, lockResource{ObjectRef: ref}, accessExclusive, relationLockPolicy{})
	if err != nil || exclusive == nil {
		t.Fatal(exclusive, err)
	}
	err = concurrentUnitRequestWait(t, exclusive)
	var diagnostic *sqlstate.Error
	first := fmt.Sprintf("Transaction %d (session %d, epoch %d) waits for AccessExclusiveLock on relation %d (class %d, sub-ID 0, epoch %d); blocked by transaction %d (session %d, epoch %d).", ta.id, ta.session.id, ta.epoch, ref.Address.ObjectID, ref.Address.ClassID, ref.Epoch, tb.id, tb.session.id, tb.epoch)
	second := fmt.Sprintf("Transaction %d (session %d, epoch %d) waits for transaction completion; blocked by transaction %d (session %d, epoch %d).", tb.id, tb.session.id, tb.epoch, ta.id, ta.session.id, ta.epoch)
	if !errors.As(err, &diagnostic) {
		t.Fatal(err)
	}
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "40P01", Message: "deadlock detected", Hint: "See server log for query details.", Detail: diagnostic.Detail}
	if !reflect.DeepEqual(*diagnostic, want) || (diagnostic.Detail != first+"\n"+second && diagnostic.Detail != second+"\n"+first) {
		t.Fatal(diagnostic)
	}
	e.locks.mu.Lock()
	hardSurvives := !completion.terminal && completion.entry.holders[a.transactions.ticket].mode == transactionCompletion
	e.locks.mu.Unlock()
	if !hardSurvives {
		t.Fatal("hard completion edge was removed or reordered as a soft relation queue")
	}
	transactionEnd(t, a, ta, false)
	if err := concurrentUnitRequestWait(t, completion); err != nil {
		t.Fatal(err)
	}
	transactionEnd(t, b, tb, false)
	lockEmpty(t, e)
}

func TestConcurrentIndexU6CompletionTerminalArbitration(t *testing.T) {
	for _, mode := range []string{"cancel", "statement_deadline", "lock_deadline", "grant", "epoch"} {
		t.Run(mode, func(t *testing.T) {
			e := New()
			a, b := sessionStart(t, e, nil), sessionStart(t, e, nil)
			ta, tb := transactionBegin(t, a), transactionBegin(t, b)
			owner, waiter := a.transactions.ticket, b.transactions.ticket
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			policy := relationLockPolicy{}
			if mode == "lock_deadline" {
				policy.timeout = time.Nanosecond
			}
			if mode == "statement_deadline" {
				policy.statementDeadline = time.Now().Add(-time.Second)
			}
			request, err := e.locks.startAcquire(ctx, waiter, lockResource{completion: owner}, transactionCompletion, policy)
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
					transactionEnd(t, a, ta, false)
					if err := concurrentUnitRequestWait(t, request); err != nil {
						t.Fatal(err)
					}
					cancel()
					// An ended exact owner is immediately complete; no new virtual authority.
					next, err := e.locks.startAcquire(context.Background(), waiter, lockResource{completion: owner}, transactionCompletion, relationLockPolicy{})
					if next != nil || err != nil {
						t.Fatal(next, err)
					}
					e.locks.mu.Lock()
					_, retained := waiter.resources[lockResource{completion: owner}]
					terminal := request.terminal && request.err == nil
					e.locks.mu.Unlock()
					if retained || !terminal {
						t.Fatal("late callback retained or retracted successful completion")
					}
				case "epoch":
					if err := e.Reset(context.Background()); err != nil {
						t.Fatal(err)
					}
					if err := concurrentUnitRequestWait(t, request); !errors.Is(err, ErrCatalogReplaced) {
						t.Fatal(err)
					}
				}
			}
			if a.TransactionState() != TxIdle {
				transactionEnd(t, a, ta, false)
			}
			transactionEnd(t, b, tb, false)
			lockEmpty(t, e)
			e.mu.Lock()
			registry := len(e.visibility)
			e.mu.Unlock()
			if registry != 0 {
				t.Fatal("terminal visibility registry leak")
			}
		})
	}
}
