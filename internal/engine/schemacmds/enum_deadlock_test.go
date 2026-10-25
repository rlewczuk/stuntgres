package schemacmds_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
)

func TestEnumTypeDeadlockRetirement(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	for _, q := range []string{"CREATE TYPE cycle_left AS ENUM ('old')", "CREATE TYPE cycle_right AS ENUM ('old')"} {
		domainUnitPublish(t, e, domainUnitStage(t, e, op, q))
	}
	done()
	var resources [2]lock.Resource
	for i, name := range []string{"cycle_left", "cycle_right"} {
		d := domainUnitType(t, e.CatalogRead(), name)
		resources[i] = lock.Resource{ObjectRef: e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), d.Address())}
	}
	sessions := [2]*session.Session{enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)}
	var refs [2]ids.TxRef
	var tickets [2]*lock.Ticket
	var pending [2]*lock.Request
	for i, s := range sessions {
		refs[i] = enginetest.TransactionBegin(t, s)
		tickets[i] = s.Transactions.Ticket
		t.Cleanup(func() { e.EndLockOwner(tickets[i]) })
		if r := deadlockRequest(t, e, tickets[i], resources[i], lock.TypeExclusive); r != nil {
			t.Fatal("initial type grant queued")
		}
	}
	for i := range pending {
		pending[i] = deadlockRequest(t, e, tickets[i], resources[1-i], lock.TypeExclusive)
		if pending[i] == nil {
			t.Fatal("opposing request granted")
		}
	}
	victim := -1
	lockState := e.Locks.Inspect()
	for i, r := range pending {
		if r.Terminal() && r.Err() != nil {
			if victim != -1 {
				t.Error("multiple type victims")
			}
			victim = i
		}
		if lockState.Resources[resources[i]].Holders[tickets[i]].Mode != lock.TypeExclusive {
			t.Error("deadlock detection released grant before transaction finish")
		}
	}
	if victim < 0 {
		t.Fatal("no terminal type victim")
	}
	var d *sqlstate.Error
	if err := deadlockResult(t, pending[victim]); !errors.As(err, &d) || d.Code != "40P01" || d.Message != "deadlock detected" || d.Hint != "See server log for query details." {
		t.Fatal(err)
	}
	want := map[string]bool{}
	for i, ticket := range tickets {
		blocker := tickets[1-i]
		resource := resources[1-i]
		want[fmt.Sprintf("Transaction %d (session %d, epoch %d) waits for ExclusiveLock on type %d (class 1247, sub-ID 0, epoch %d); blocked by transaction %d (session %d, epoch %d).", ticket.Ref().ID(), ticket.Ref().Session().ID(), ticket.Ref().Epoch(), resource.Address.ObjectID, resource.Epoch, blocker.Ref().ID(), blocker.Ref().Session().ID(), blocker.Ref().Epoch())] = true
	}
	for _, line := range strings.Split(d.Detail, "\n") {
		if !want[line] {
			t.Fatal("wrong type cycle edge", line)
		}
		delete(want, line)
	}
	if len(want) != 0 {
		t.Fatal("missing type cycle edge", want)
	}
	enginetest.TransactionEnd(t, sessions[victim], refs[victim], false)
	survivor := 1 - victim
	if err := deadlockResult(t, pending[survivor]); err != nil {
		t.Fatal("survivor failed", err)
	}
	enginetest.TransactionEnd(t, sessions[survivor], refs[survivor], true)
	enginetest.LockEmpty(t, e)
	for _, s := range sessions {
		ref := enginetest.TransactionBegin(t, s)
		for _, resource := range resources {
			if err := e.Locks.Acquire(context.Background(), s.Transactions.Ticket, resource, lock.TypeExclusive); err != nil {
				t.Fatal("retired cycle blocked fresh owner", err)
			}
		}
		enginetest.TransactionEnd(t, s, ref, true)
	}
	enginetest.LockEmpty(t, e)
}

func deadlockRequest(t *testing.T, e *core.Engine, ticket *lock.Ticket, resource lock.Resource, mode lock.Mode) *lock.Request {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	t.Cleanup(cancel)
	r, err := e.Locks.StartAcquire(ctx, ticket, resource, mode, lock.Policy{CompletionPurpose: lock.CompletionVirtual})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func deadlockResult(t *testing.T, r *lock.Request) error {
	t.Helper()
	if r == nil {
		return nil
	}
	if deadlockGuardCompleted(r.Done()) {
		return r.Err()
	}
	guard := time.NewTimer(time.Second)
	defer guard.Stop()
	if !deadlockAwait(r.Done(), guard.C) {
		t.Fatal("request did not terminate before failure guard")
	}
	return r.Err()
}

func deadlockGuardCompleted(done <-chan struct{}) bool {
	select {
	case <-done:
		return true
	default:
		return false
	}
}

func deadlockAwait(done <-chan struct{}, guard <-chan time.Time) bool {
	if deadlockGuardCompleted(done) {
		return true
	}
	select {
	case <-done:
		return true
	case <-guard:
		return deadlockGuardCompleted(done)
	}
}
