package core_test

import (
	"context"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/lock"
)

func TestAdvisorySessionPhysicalSoftCycle(t *testing.T) {
	// Adapt the existing concrete relation soft-queue fixture, with advisory X
	// and ordinary Y. The cycle exists only after physical-ticket collapsing.
	e := engine.New()
	a := enginetest.StartSession(t, e, nil)
	b := enginetest.StartSession(t, e, nil)
	c := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, a, "CREATE TABLE advisory_soft_relation(v INT)")
	ra, rb, rc := enginetest.TransactionBegin(t, a), enginetest.TransactionBegin(t, b), enginetest.TransactionBegin(t, c)
	x := lock.Resource{Database: e.Catalog.Domain, Kind: lock.AdvisoryBigintResource, High: 21, Low: 22}
	y := lock.Resource{ObjectRef: enginetest.CatalogRef(t, e, e.CatalogRead(), "advisory_soft_relation")}
	advisoryUnitGrant(t, a, ra, x, lock.AdvisoryShare)
	if err := e.Locks.Acquire(context.Background(), c.Transactions.Ticket, y, lock.AccessExclusive); err != nil {
		t.Fatal(err)
	}
	writer, err := advisoryUnitAcquire(t, b, rb, x, lock.AdvisoryExclusive, false)
	if err != nil || writer == nil {
		t.Fatal(writer, err)
	}
	reader, err := advisoryUnitAcquire(t, c, rc, x, lock.AdvisoryShare, false)
	if err != nil || reader == nil {
		t.Fatal(reader, err)
	}
	lockState := e.Locks.Inspect()
	queue := lockState.Resources[x].Pending
	ordered := len(queue) == 2 && queue[0].Request == writer && queue[1].Request == reader
	if !ordered {
		t.Fatal("exact soft predecessor queue absent")
	}
	ordinaryWait := deadlockRequest(t, e, a.Transactions.Ticket, y, lock.AccessShare)
	if ordinaryWait == nil {
		t.Fatal("ordinary holder dependency absent")
	}
	if err = deadlockResult(t, reader); err != nil {
		t.Fatal("resolvable physical soft cycle aborted", err)
	}
	lockState = e.Locks.Inspect()
	writerPending := !writer.Terminal()
	ordinaryPending := !ordinaryWait.Terminal()
	if !writerPending || !ordinaryPending || advisoryUnitHold(a, x).Shared != 1 || advisoryUnitHold(c, x).Shared != 1 {
		t.Fatal("rearrangement revoked holder or granted incompatible waiter")
	}
	enginetest.TransactionEnd(t, c, rc, false)
	if err = deadlockResult(t, ordinaryWait); err != nil {
		t.Fatal(err)
	}
	// Releasing ordinary transactions does not erase the advisory contributions
	// that still block the writer, even after successful soft-cycle repair.
	enginetest.TransactionEnd(t, a, ra, false)
	lockState = e.Locks.Inspect()
	writerPending = !writer.Terminal()
	if !writerPending || advisoryUnitHold(a, x).Shared != 1 || advisoryUnitHold(c, x).Shared != 1 {
		t.Fatal("transaction release crossed persistent lifetime")
	}
	advisoryUnitUnlock(t, c, x, lock.AdvisoryShare, true)
	advisoryUnitUnlock(t, a, x, lock.AdvisoryShare, true)
	if err = deadlockResult(t, writer); err != nil || advisoryUnitHold(b, x).Exclusive != 1 {
		t.Fatal("explicit retained-count cleanup did not wake writer", err)
	}
}

func deadlockRequest(t *testing.T, e *engine.Engine, ticket *lock.Ticket, resource lock.Resource, mode lock.Mode) *lock.Request {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	t.Cleanup(cancel)
	r, err := e.Locks.StartAcquire(ctx, ticket, resource, mode, lock.Policy{CompletionPurpose: lock.CompletionVirtual})
	if err != nil {
		t.Fatal(err)
	}
	return r
}
