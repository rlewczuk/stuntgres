package core_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/lock"
)

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
