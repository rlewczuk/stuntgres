package engine

import (
	"context"
	"testing"
)

func TestAdvisorySessionPhysicalSoftCycle(t *testing.T) {
	// Adapt the existing concrete relation soft-queue fixture, with advisory X
	// and ordinary Y. The cycle exists only after physical-ticket collapsing.
	e := New()
	a := sessionStart(t, e, nil)
	b := sessionStart(t, e, nil)
	c := sessionStart(t, e, nil)
	sessionSQL(t, a, "CREATE TABLE advisory_soft_relation(v INT)")
	ra, rb, rc := transactionBegin(t, a), transactionBegin(t, b), transactionBegin(t, c)
	x := lockResource{Kind: advisoryBigintResource, High: 21, Low: 22}
	y := lockResource{ObjectRef: catalogRef(t, e, e.CatalogRead(), "advisory_soft_relation")}
	advisoryUnitGrant(t, a, ra, x, advisoryShared)
	if err := e.locks.Acquire(context.Background(), c.transactions.ticket, y, accessExclusive); err != nil {
		t.Fatal(err)
	}
	writer, err := advisoryUnitAcquire(t, b, rb, x, advisoryExclusive, false)
	if err != nil || writer == nil {
		t.Fatal(writer, err)
	}
	reader, err := advisoryUnitAcquire(t, c, rc, x, advisoryShared, false)
	if err != nil || reader == nil {
		t.Fatal(reader, err)
	}
	e.locks.mu.Lock()
	queue := e.locks.entries[x].pending
	ordered := len(queue) == 2 && queue[0] == writer && queue[1] == reader
	e.locks.mu.Unlock()
	if !ordered {
		t.Fatal("exact soft predecessor queue absent")
	}
	ordinaryWait := deadlockRequest(t, e, a.transactions.ticket, y, accessShare)
	if ordinaryWait == nil {
		t.Fatal("ordinary holder dependency absent")
	}
	if err = deadlockResult(t, reader); err != nil {
		t.Fatal("resolvable physical soft cycle aborted", err)
	}
	e.locks.mu.Lock()
	writerPending := !writer.terminal
	ordinaryPending := !ordinaryWait.terminal
	e.locks.mu.Unlock()
	if !writerPending || !ordinaryPending || advisoryUnitHold(a, x).Shared != 1 || advisoryUnitHold(c, x).Shared != 1 {
		t.Fatal("rearrangement revoked holder or granted incompatible waiter")
	}
	transactionEnd(t, c, rc, false)
	if err = deadlockResult(t, ordinaryWait); err != nil {
		t.Fatal(err)
	}
	// Releasing ordinary transactions does not erase the advisory contributions
	// that still block the writer, even after successful soft-cycle repair.
	transactionEnd(t, a, ra, false)
	e.locks.mu.Lock()
	writerPending = !writer.terminal
	e.locks.mu.Unlock()
	if !writerPending || advisoryUnitHold(a, x).Shared != 1 || advisoryUnitHold(c, x).Shared != 1 {
		t.Fatal("transaction release crossed persistent lifetime")
	}
	advisoryUnitUnlock(t, c, x, advisoryShared, true)
	advisoryUnitUnlock(t, a, x, advisoryShared, true)
	if err = deadlockResult(t, writer); err != nil || advisoryUnitHold(b, x).Exclusive != 1 {
		t.Fatal("explicit retained-count cleanup did not wake writer", err)
	}
}
