package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"stuntgres/internal/engine/lock"
)

// A queued session-level advisory request blocks behind its holder's session
// but is not a safe-snapshot wait. The journal and provenance authority behind
// this inquiry is covered by the lock manager's own unit tests.
func TestBlockingPidsAdvisorySafeSnapshot(t *testing.T) {
	e := New()
	a, ra := advisoryUnitOwner(t, e)
	b, rb := advisoryUnitOwner(t, e)
	key := lock.Resource{Kind: lock.AdvisoryBigintResource, Low: 187418}
	advisoryUnitGrant(t, a, ra, key, lock.AdvisoryExclusive)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r, err := e.locks.StartAcquire(ctx, b.advisory.ticket, key, lock.AdvisoryExclusive, lock.Policy{Provenance: rb})
	if err != nil || r == nil {
		t.Fatal("persistent advisory request", r, err)
	}
	blockingUnitWant(t, e, b.token.ID(), int64(a.token.ID()))
	blockingUnitSafeWant(t, e, b.token.ID())
	transactionEnd(t, b, rb, false)
	if err := deadlockResult(t, r); !errors.Is(err, lock.ErrOwnerEnded) {
		t.Fatal("aborted provenance", err)
	}
	blockingUnitWant(t, e, b.token.ID())
	advisoryUnitUnlock(t, a, key, lock.AdvisoryExclusive, true)
}
