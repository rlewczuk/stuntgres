package enginetests_test

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
)

// pgLocksUnitWaiter is the queued copy of r, or the zero waiter once r left
// its queue.
func pgLocksUnitWaiter(e *core.Engine, r *lock.Request) lock.Waiter {
	if state := e.Locks.Inspect().Resources[r.Resource()]; state != nil {
		for _, w := range state.Pending {
			if w.Request == r {
				return w
			}
		}
	}
	return lock.Waiter{}
}

// pgLocksUnitKey is a unique-key claim that equals no stored key.
type pgLocksUnitKey struct{}

func (pgLocksUnitKey) EqualKey(context.Context, any) (bool, error) { return false, nil }
func (pgLocksUnitKey) OwnedKey() any                               { return nil }

// pgLocksUnitKeyGrant interns a new unique key of the unit relation and grants
// ticket its claim, pinning the key only across the acquisition as the
// engine's unique-key path does.
func pgLocksUnitKeyGrant(t *testing.T, e *core.Engine, ticket *lock.Ticket) lock.Resource {
	t.Helper()
	resource, unpin, err := e.Locks.PinUniqueKey(context.Background(), ticket, blockingUnitResource(e).ObjectRef, 17, pgLocksUnitKey{})
	if err != nil {
		t.Fatal(err)
	}
	defer unpin()
	if err := e.Locks.Acquire(context.Background(), ticket, resource, lock.UniqueClaim); err != nil {
		t.Fatal(err)
	}
	return resource
}

func pgLocksUnitRequest(t *testing.T, e *core.Engine, ticket *lock.Ticket, resource lock.Resource, mode lock.Mode, purpose lock.CompletionPurpose) (*lock.Request, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	r, err := e.Locks.StartAcquire(ctx, ticket, resource, mode, lock.Policy{CompletionPurpose: purpose})
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		if r != nil {
			select {
			case <-r.Done():
			case <-time.After(time.Second):
				t.Error("pending request did not join")
			}
		}
	})
	return r, cancel
}

func pgLocksUnitWait(t *testing.T, e *core.Engine, ticket *lock.Ticket) lock.Record {
	t.Helper()
	var waits []lock.Record
	for _, r := range pgLocksUnitCapture(t, e) {
		if r.Waiting && r.Owner.Session == ticket.Ref().Session().ID() {
			waits = append(waits, r)
		}
	}
	if len(waits) != 1 {
		t.Fatal("one real public wait", waits)
	}
	return waits[0]
}

func TestPGLocksWaitDependencies(t *testing.T) {
	t.Run("multi_holder_retarget_preserves_enqueue", func(t *testing.T) {
		e := engine.New()
		_, a := blockingUnitOwner(t, e)
		_, b := blockingUnitOwner(t, e)
		_, waiter := blockingUnitOwner(t, e)
		resource := blockingUnitResource(e)
		resource.Row = 1
		for _, ticket := range []*lock.Ticket{a, b} {
			if err := e.Locks.Acquire(context.Background(), ticket, resource, lock.ForKeyShare); err != nil {
				t.Fatal(err)
			}
		}
		first, second := pgLocksUnitAssign(t, e, a), pgLocksUnitAssign(t, e, b)
		r, _ := pgLocksUnitRequest(t, e, waiter, resource, lock.ForUpdate, lock.CompletionTransaction)
		if r == nil {
			t.Fatal("UPDATE failed to queue")
		}
		before := pgLocksUnitWait(t, e, waiter)
		if before.Kind != lock.TransactionRecord || before.XID != first || before.WaitStart != pgLocksUnitWaiter(e, r).EnqueuedAt {
			t.Fatal("first actual holder", before)
		}
		if again := pgLocksUnitWait(t, e, waiter); again != before {
			t.Fatal("unstable observation", before, again)
		}
		e.EndLockOwner(a)
		after := pgLocksUnitWait(t, e, waiter)
		if after.XID != second || after.WaitStart != before.WaitStart || r.Terminal() {
			t.Fatal("multi-holder retarget/grant", after, r.Terminal())
		}
		e.EndLockOwner(b)
		if err := deadlockResult(t, r); err != nil {
			t.Fatal(err)
		}
		for _, r := range pgLocksUnitCapture(t, e) {
			if r.Waiting || r.Kind == lock.RelationRecord && r.Object == uint32(resource.Address.ObjectID) {
				t.Fatal("durable row hold became tuple/relation resource", r)
			}
		}
	})
	t.Run("queue_only_genuine_VXID_and_no_key_update_bypass", func(t *testing.T) {
		e := engine.New()
		_, a := blockingUnitOwner(t, e)
		_, b := blockingUnitOwner(t, e)
		_, c := blockingUnitOwner(t, e)
		_, bypass := blockingUnitOwner(t, e)
		resource := blockingUnitResource(e)
		resource.Row = 1
		if err := e.Locks.Acquire(context.Background(), a, resource, lock.ForKeyShare); err != nil {
			t.Fatal(err)
		}
		br, cancelB := pgLocksUnitRequest(t, e, b, resource, lock.ForUpdate, lock.CompletionTransaction)
		cr, _ := pgLocksUnitRequest(t, e, c, resource, lock.ForKeyShare, lock.CompletionTransaction)
		if br == nil || cr == nil {
			t.Fatal("missing actual queue-only predecessor")
		}
		before := pgLocksUnitWait(t, e, c)
		if before.Kind != lock.VirtualRecord || before.Target.Session != b.Ref().Session().ID() || before.Target.Local != b.Ref().ID() || before.XID != 0 {
			t.Fatal("queue-only target fabricated/dropped", before)
		}
		if r, _ := pgLocksUnitRequest(t, e, bypass, resource, lock.ForNoKeyUpdate, lock.CompletionTransaction); r != nil {
			t.Fatal("NO KEY UPDATE bypass was changed")
		}
		// Snapshot cannot settle; both actual queued requests remain journaled.
		if br.Terminal() || cr.Terminal() {
			t.Fatal("observer changed acquisition state")
		}
		cancelB()
		if err := deadlockResult(t, br); !errors.Is(err, context.Canceled) {
			t.Fatal("queue cancel", err)
		}
		if err := deadlockResult(t, cr); err != nil {
			t.Fatal("compatible tail did not grant", err)
		}
		for _, r := range pgLocksUnitCapture(t, e) {
			if r.Waiting && r.Owner.Session == c.Ref().Session().ID() {
				t.Fatal("queue-only phantom after grant", r)
			}
		}
	})
	t.Run("unique_identity_and_missing_XID_transition", func(t *testing.T) {
		e := engine.New()
		_, a := blockingUnitOwner(t, e)
		_, b := blockingUnitOwner(t, e)
		_, c := blockingUnitOwner(t, e)
		// A genuine interned key with an arbitrary hash. No SQL value or hash is
		// consulted by snapshotting or becomes a public target.
		resource := pgLocksUnitKeyGrant(t, e, a)
		br, _ := pgLocksUnitRequest(t, e, b, resource, lock.UniqueClaim, lock.CompletionTransaction)
		cr, _ := pgLocksUnitRequest(t, e, c, resource, lock.UniqueClaim, lock.CompletionTransaction)
		before := pgLocksUnitWait(t, e, b)
		if before.Kind != lock.VirtualRecord || before.Target.Session != a.Ref().Session().ID() {
			t.Fatal("unassigned unique holder", before)
		}
		xid := pgLocksUnitAssign(t, e, a)
		after := pgLocksUnitWait(t, e, b)
		if after.Kind != lock.TransactionRecord || after.XID != xid || after.WaitStart != before.WaitStart {
			t.Fatal("assignment transition", after)
		}
		pgLocksUnitAssign(t, e, b)
		e.EndLockOwner(a)
		if err := deadlockResult(t, br); err != nil {
			t.Fatal(err)
		}
		tail := pgLocksUnitWait(t, e, c)
		if tail.XID != b.AssignedPublicXID() || cr.Terminal() {
			t.Fatal("unique dependency retarget", tail)
		}
		e.EndLockOwner(b)
		if err := deadlockResult(t, cr); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("index_wait_target_prepares_after_enqueue", func(t *testing.T) {
		e := engine.New()
		_, holder := blockingUnitOwner(t, e)
		_, waiter := blockingUnitOwner(t, e)
		xid := pgLocksUnitAssign(t, e, holder)
		request, _ := pgLocksUnitRequest(t, e, waiter, lock.CompletionOf(holder), lock.TransactionCompletion, lock.CompletionVirtual)
		if request == nil {
			t.Fatal("index completion did not enqueue")
		}
		before := pgLocksUnitWait(t, e, waiter)
		if before.Kind != lock.VirtualRecord || before.Target.Local != holder.Ref().ID() || before.Target.Session != holder.Ref().Session().ID() {
			t.Fatal("live index completion target", before)
		}
		e.Locks.DetachPrepared(holder)
		after := pgLocksUnitWait(t, e, waiter)
		if after.Kind != lock.TransactionRecord || after.XID != xid || after.WaitStart != before.WaitStart || after.WaitStart != pgLocksUnitWaiter(e, request).EnqueuedAt {
			t.Fatal("prepared target did not retarget same original wait", before, after)
		}
		if request.Resource().Completion() != holder || pgLocksUnitWaiter(e, request).Policy.CompletionPurpose != lock.CompletionVirtual || request.Terminal() {
			t.Fatal("observation replaced or settled completion request")
		}
		records := pgLocksUnitCapture(t, e)
		if len(pgLocksUnitOwnerRecords(records, holder, lock.VirtualRecord)) != 0 {
			t.Fatal("prepared target retained retired VXID", records)
		}
		xids := pgLocksUnitOwnerRecords(records, holder, lock.TransactionRecord)
		if len(xids) != 1 || !xids[0].Owner.Prepared || xids[0].XID != xid {
			t.Fatal("prepared target lost genuine XID ownership", records)
		}
		// Real completion publication can precede atomic manager release.
		// A still-journaled prepared wait must retain its genuine target here.
		e.Mu.Lock()
		e.Visibility.RecordCommitTimestampLocked(holder.Ref())
		e.Mu.Unlock()
		if gap := pgLocksUnitWait(t, e, waiter); gap != after {
			t.Fatal("prepared publication-to-manager-end gap changed dependency", after, gap)
		}
		e.EndLockOwner(holder)
		if err := deadlockResult(t, request); err != nil {
			t.Fatal(err)
		}
		for _, record := range pgLocksUnitCapture(t, e) {
			if record.Waiting {
				t.Fatal("finished prepared completion retained pending row", record)
			}
		}
	})
	for _, purpose := range []lock.CompletionPurpose{lock.CompletionVirtual, lock.CompletionTransaction} {
		t.Run(map[lock.CompletionPurpose]string{lock.CompletionVirtual: "index_completion_VXID", lock.CompletionTransaction: "exclusion_completion_XID"}[purpose], func(t *testing.T) {
			e := engine.New()
			_, holder := blockingUnitOwner(t, e)
			_, waiter := blockingUnitOwner(t, e)
			xid := pgLocksUnitAssign(t, e, holder)
			r, _ := pgLocksUnitRequest(t, e, waiter, lock.CompletionOf(holder), lock.TransactionCompletion, purpose)
			pending := pgLocksUnitWait(t, e, waiter)
			if purpose == lock.CompletionVirtual && (pending.Kind != lock.VirtualRecord || pending.Target.Local != holder.Ref().ID()) || purpose == lock.CompletionTransaction && (pending.Kind != lock.TransactionRecord || pending.XID != xid) {
				t.Fatal("caller-selected completion purpose", pending)
			}
			captured := pgLocksUnitCapture(t, e)
			if len(pgLocksUnitOwnerRecords(captured, holder, lock.TransactionRecord)) != 1 || len(pgLocksUnitOwnerRecords(captured, holder, lock.VirtualRecord)) != 1 {
				t.Fatal("lazy completion duplicated exclusive ownership", captured)
			}
			e.EndLockOwner(holder)
			if err := deadlockResult(t, r); err != nil {
				t.Fatal(err)
			}
			for _, r := range pgLocksUnitCapture(t, e) {
				if r.Waiting || r.Owner.Session == waiter.Ref().Session().ID() && (r.Kind == lock.TransactionRecord || r.Mode == lock.TransactionCompletion) {
					t.Fatal("transient completion retained ShareLock", r)
				}
			}
		})
	}
}

func TestPGLocksSnapshotOwnership(t *testing.T) {
	for _, ending := range []string{"cancel", "grant", "fence"} {
		t.Run(ending, func(t *testing.T) {
			e := engine.New()
			_, a := blockingUnitOwner(t, e)
			_, b := blockingUnitOwner(t, e)
			resource := blockingUnitResource(e)
			if err := e.Locks.Acquire(context.Background(), a, resource, lock.AccessExclusive); err != nil {
				t.Fatal(err)
			}
			r, cancel := pgLocksUnitRequest(t, e, b, resource, lock.AccessExclusive, lock.CompletionVirtual)
			if r == nil {
				t.Fatal("missing owned request")
			}
			before := pgLocksUnitCapture(t, e)
			saved := append([]lock.Record(nil), before...)
			// Mutation of caller-owned scalars cannot alter the authoritative queue.
			for i := range before {
				before[i].Owner.Session = 0
				before[i].Mode = 0
			}
			if current := pgLocksUnitCapture(t, e); !pgLocksUnitBagEqual(saved, current) {
				t.Fatal("snapshot aliases manager", saved, current)
			}
			var wg sync.WaitGroup
			wg.Add(1)
			go func() {
				defer wg.Done()
				switch ending {
				case "cancel":
					cancel()
				case "grant":
					e.EndLockOwner(a)
				case "fence":
					e.Locks.Fence(e.Catalog.Domain, a.Ref().Epoch()+1)
				}
			}()
			for range 32 {
				// A capture is coherent at its manager linearization point. A
				// terminal outcome after capture need not invalidate owned history.
				_, err := e.Locks.PGLocks(e.Catalog.Domain, a.Ref().Epoch())
				if err != nil && !errors.Is(err, ids.ErrCatalogReplaced) {
					t.Error(err)
				}
			}
			wg.Wait()
			err := deadlockResult(t, r)
			if ending == "grant" && err != nil || ending == "cancel" && !errors.Is(err, context.Canceled) || ending == "fence" && !errors.Is(err, ids.ErrCatalogReplaced) {
				t.Fatal("terminal outcome", ending, err)
			}
			epoch := a.Ref().Epoch()
			if ending == "fence" {
				epoch++
			}
			records, err := e.Locks.PGLocks(e.Catalog.Domain, epoch)
			if err != nil {
				t.Fatal(err)
			}
			for _, r := range records {
				if r.Waiting {
					t.Fatal("terminal pending row leaked", r)
				}
			}
			if len(saved) == 0 || reflect.DeepEqual(before, saved) {
				t.Fatal("test did not preserve/mutate distinct owned capture")
			}
		})
	}
}

func pgLocksUnitBagEqual(a, b []lock.Record) bool {
	if len(a) != len(b) {
		return false
	}
	for _, want := range a {
		found := false
		for _, got := range b {
			if got == want {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func blockingUnitResource(e *core.Engine) lock.Resource {
	return lock.Resource{ObjectRef: e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 999999})}
}

func pgLocksUnitCapture(t *testing.T, e *core.Engine) []lock.Record {
	t.Helper()
	records, err := e.Locks.PGLocks(e.Catalog.Domain, e.Catalog.State.Epoch())
	if err != nil {
		t.Fatal(err)
	}
	return records
}

func blockingUnitOwner(t *testing.T, e *core.Engine) (*session.Session, *lock.Ticket) {
	t.Helper()
	s := enginetest.StartSession(t, e, nil)
	enginetest.TransactionBegin(t, s)
	return s, s.Transactions.Ticket
}

func pgLocksUnitAssign(t *testing.T, e *core.Engine, ticket *lock.Ticket) uint32 {
	t.Helper()
	e.Mu.Lock()
	err := e.AssignTransactionXIDLocked(ticket.Ref())
	xid := e.Visibility.Entries[ticket.Ref()].AssignedXID
	e.Mu.Unlock()
	if err != nil || xid == 0 || ticket.AssignedPublicXID() != xid {
		t.Fatal("canonical assignment mirror", xid, ticket.AssignedPublicXID(), err)
	}
	return xid
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

func pgLocksUnitOwnerRecords(records []lock.Record, ticket *lock.Ticket, kind lock.RecordKind) []lock.Record {
	var result []lock.Record
	for _, r := range records {
		if r.Owner.Session == ticket.Ref().Session().ID() && r.Owner.Local == ticket.Ref().ID() && r.Kind == kind {
			result = append(result, r)
		}
	}
	return result
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
