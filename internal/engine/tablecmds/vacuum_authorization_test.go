package tablecmds_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/tablecmds"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func vacuumLeaseCheckpoint(t *testing.T) {
	e := engine.New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE TABLE lease_target(id INT)")
	sessionSQL(t, s, "CREATE TABLE lease_prior(id INT)")
	target, _ := vacuumStore(t, e, "lease_target")
	prior, _ := vacuumStore(t, e, "lease_prior")
	op := transactionOperation(t, s)
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	raw := enginetest.Statement(t, "VACUUM lease_target").(*parser.Vacuum)
	if err := scope.Gate(raw); err != nil {
		scope.Finish(err)
		t.Fatal(err)
	}
	ticket := scope.LockTicket
	ref := scope.TxRef
	if err := e.Locks.Acquire(context.Background(), ticket, lock.Resource{ObjectRef: prior}, lock.AccessShare); err != nil {
		scope.Finish(err)
		t.Fatal(err)
	}
	_, err = tablecmds.ExecuteVacuum(e, scope, raw, 0)
	if err != nil {
		scope.Finish(err)
		t.Fatal(err)
	}
	state := e.Locks.Inspect(ticket)
	if state.Resources[lock.Resource{ObjectRef: target}] != nil {
		scope.Finish(nil)
		t.Fatal("per-relation checkpoint retained new target grant")
	}
	hold := state.Resources[lock.Resource{ObjectRef: prior}]
	if hold == nil || hold.Holders[ticket].Mode != lock.AccessShare || ticket.OwnershipEnded() {
		scope.Finish(nil)
		t.Fatal("checkpoint discarded preexisting owner/grant")
	}
	if err := scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	e.Mu.Lock()
	_, registered := e.Visibility.Entries[ref]
	progress := len(e.VacuumProgress)
	e.Mu.Unlock()
	if registered || progress != 0 || !ticket.OwnershipEnded() {
		t.Fatal("final scope failed canonical owner cleanup")
	}
	lockEmpty(t, e)
}
func vacuumGrantTimeRecheck(t *testing.T) {
	e := engine.New()
	owner, holder, worker := sessionStart(t, e, nil), sessionStart(t, e, nil), sessionStart(t, e, nil)
	for _, q := range []string{"CREATE ROLE vacuum_recheck_actor", "CREATE TABLE recheck(id INT)", "INSERT INTO recheck VALUES(1),(2)", "DELETE FROM recheck WHERE id=1", "GRANT MAINTAIN ON recheck TO vacuum_recheck_actor"} {
		sessionSQL(t, owner, q)
	}
	sessionSQL(t, worker, "SET ROLE vacuum_recheck_actor")
	ref, store := vacuumStore(t, e, "recheck")
	transactionBegin(t, holder)
	if err := e.Locks.Acquire(context.Background(), holder.Transactions.Ticket, lock.Resource{ObjectRef: ref}, lock.Share); err != nil {
		t.Fatal(err)
	}
	// Genuine NoWait unavailability skips without enqueuing a request or progress.
	op := transactionOperation(t, worker)
	result, err := e.Execute(op, enginetest.Statement(t, "VACUUM (SKIP_LOCKED) recheck"), 0, nil)
	notices := op.DetachNotices()
	op.Done()
	if err != nil || result.Count != 0 || result.Rows != nil || result.CommandTag != "VACUUM" {
		t.Fatal(result, err)
	}
	want := sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "55P03", Message: `skipping vacuum of "recheck" --- lock not available`}
	if len(notices) != 1 || notices[0] != want {
		t.Fatalf("NoWait warnings %#v want %#v", notices, want)
	}
	if entry := e.Locks.Inspect().Resources[lock.Resource{ObjectRef: ref}]; entry == nil || len(entry.Pending) != 0 {
		t.Fatal("NoWait queued a request", entry)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := lockAsync(e, worker, ctx, "VACUUM recheck", false)
	joined := false
	defer func() {
		cancel()
		if !joined {
			lockAwait(t, done, context.Canceled)
		}
	}()
	lockPending(t, e, 1)
	sessionSQL(t, owner, "REVOKE MAINTAIN ON recheck FROM vacuum_recheck_actor")
	sessionSQL(t, holder, "ROLLBACK")
	lockAwait(t, done, nil)
	joined = true
	if store.Maintenance.VacuumCount != 0 || len(store.Versions) != 2 || len(store.Ends.Rows) != 1 {
		t.Fatal("grant-time revoke admitted stale maintenance")
	}
	lockEmpty(t, e)
}
