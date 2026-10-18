package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestEnumTypeDeadlockRetirement(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	for _, q := range []string{"CREATE TYPE cycle_left AS ENUM ('old')", "CREATE TYPE cycle_right AS ENUM ('old')"} {
		domainUnitPublish(t, e, domainUnitStage(t, e, op, q))
	}
	done()
	var resources [2]lock.Resource
	for i, name := range []string{"cycle_left", "cycle_right"} {
		d := domainUnitType(t, e.CatalogRead(), name)
		resources[i] = lock.Resource{ObjectRef: e.catalog.Domain.Ref(e.catalog.state.epoch, d.Address())}
	}
	sessions := [2]*Session{sessionStart(t, e, nil), sessionStart(t, e, nil)}
	var refs [2]ids.TxRef
	var tickets [2]*lock.Ticket
	var pending [2]*lock.Request
	for i, s := range sessions {
		refs[i] = transactionBegin(t, s)
		tickets[i] = s.transactions.ticket
		t.Cleanup(func() { e.endLockOwner(tickets[i]) })
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
	lockState := e.locks.Inspect()
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
	transactionEnd(t, sessions[victim], refs[victim], false)
	survivor := 1 - victim
	if err := deadlockResult(t, pending[survivor]); err != nil {
		t.Fatal("survivor failed", err)
	}
	transactionEnd(t, sessions[survivor], refs[survivor], true)
	lockEmpty(t, e)
	for _, s := range sessions {
		ref := transactionBegin(t, s)
		for _, resource := range resources {
			if err := e.locks.Acquire(context.Background(), s.transactions.ticket, resource, lock.TypeExclusive); err != nil {
				t.Fatal("retired cycle blocked fresh owner", err)
			}
		}
		transactionEnd(t, s, ref, true)
	}
	lockEmpty(t, e)
}
