package core_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/lock"
)

func pgrowlocksUnitCapture(t *testing.T, e *core.Engine, caller *lock.Ticket, tuple lock.MultiTuple, private bool) []lock.PgrowlocksRecord {
	t.Helper()
	rows, err := e.Locks.Pgrowlocks(context.Background(), caller, []lock.PgrowlocksTuple{{Tuple: tuple, PrivateInsertion: private}})
	if err != nil {
		t.Fatal(err)
	}
	return rows
}
func TestPgrowlocksCapture(t *testing.T) {
	e := engine.New()
	as, a := blockingUnitOwner(t, e)
	bs, b := blockingUnitOwner(t, e)
	_, observer := blockingUnitOwner(t, e)
	ax := pgLocksUnitAssign(t, e, a)
	bx := pgLocksUnitAssign(t, e, b)
	tuple := identityMultiUnitTuple(e, 1)
	identityMultiGrant(t, e, a, tuple, lock.ForKeyShare)
	identityMultiEmit(t, e, a, tuple, lock.MultiKeyShare)
	identityMultiGrant(t, e, b, tuple, lock.ForShare)
	identityMultiEmit(t, e, b, tuple, lock.MultiShare)
	before := e.Locks.Inspect()
	frontier := e.Visibility.LastAssignedXID()
	next := e.Locks.NextMultiXact(e.Catalog.Domain)
	rows := pgrowlocksUnitCapture(t, e, observer, tuple, false)
	if len(rows) != 1 || !rows[0].Multi || rows[0].Locker != before.MultiRows[tuple].ID || len(rows[0].Members) != 2 {
		t.Fatal(rows)
	}
	want := []lock.PgrowlocksMember{{XID: ax, Mode: lock.MultiKeyShare, PID: int32(a.Session().ID())}, {XID: bx, Mode: lock.MultiShare, PID: int32(b.Session().ID())}}
	if !reflect.DeepEqual(rows[0].Members, want) {
		t.Fatal(rows[0].Members, want)
	}
	if observer.AssignedPublicXID() != 0 || e.Visibility.LastAssignedXID() != frontier || e.Locks.NextMultiXact(e.Catalog.Domain) != next || !reflect.DeepEqual(before, e.Locks.Inspect()) {
		t.Fatal("capture mutated lock/XID authority")
	}
	wrong := tuple
	wrong.Version++
	if got := pgrowlocksUnitCapture(t, e, observer, wrong, false); len(got) != 0 {
		t.Fatal("RowID substituted for exact version", got)
	}
	_, waiter := blockingUnitOwner(t, e)
	pgLocksUnitAssign(t, e, waiter)
	ctx, cancel := context.WithCancel(context.Background())
	request, err := e.Locks.StartAcquire(ctx, waiter, tuple.Resource, lock.ForUpdate, lock.Policy{})
	if err != nil || request == nil {
		cancel()
		t.Fatal("real conflicting waiter", request, err)
	}
	queued := pgrowlocksUnitCapture(t, e, observer, tuple, false)
	if !reflect.DeepEqual(rows, queued) {
		cancel()
		t.Fatal("waiter appeared as tuple owner", queued)
	}
	cancel()
	if err := request.Wait(); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	enginetest.TransactionEnd(t, bs, b.Ref(), false)
	ended := pgrowlocksUnitCapture(t, e, observer, tuple, false)
	if len(ended) != 1 || !ended[0].Multi || ended[0].Locker != rows[0].Locker || len(ended[0].Members) != 2 || ended[0].Members[1].PID != 0 || ended[0].Members[0].PID != want[0].PID {
		t.Fatal("raw ended member/PID", ended)
	}
	enginetest.TransactionBegin(t, bs)
	pgLocksUnitAssign(t, e, bs.Transactions.Ticket)
	fresh := pgrowlocksUnitCapture(t, e, observer, tuple, false)
	if fresh[0].Members[1].PID != 0 {
		t.Fatal("new transaction revived departed XID", fresh)
	}
	if !reflect.DeepEqual(rows[0].Members, want) {
		t.Fatal("retained output mutated", rows)
	}
	e.Locks.DetachPrepared(a)
	detached := pgrowlocksUnitCapture(t, e, observer, tuple, false)
	if detached[0].Members[0].PID != 0 {
		t.Fatal("prepared owner retained backend PID", detached)
	}
	enginetest.TransactionEnd(t, as, a.Ref(), false)
	if got := pgrowlocksUnitCapture(t, e, observer, tuple, false); len(got) != 0 {
		t.Fatal("ended-only raw bag reported", got)
	}
}
