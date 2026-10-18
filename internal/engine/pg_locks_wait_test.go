package engine

import (
	"context"
	"errors"
	"reflect"
	"runtime"
	"strconv"
	"sync"
	"testing"
	"time"

	"stuntgres/internal/engine/ids"
)

func pgLocksUnitRequest(t *testing.T, e *Engine, ticket *relationTicket, resource lockResource, mode relationLockMode, purpose completionLockPurpose) (*relationRequest, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	r, err := e.locks.StartAcquire(ctx, ticket, resource, mode, relationLockPolicy{CompletionPurpose: purpose})
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

func pgLocksUnitWait(t *testing.T, e *Engine, ticket *relationTicket) pgLockRecord {
	t.Helper()
	var waits []pgLockRecord
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
		e := New()
		_, a := blockingUnitOwner(t, e)
		_, b := blockingUnitOwner(t, e)
		_, waiter := blockingUnitOwner(t, e)
		resource := blockingUnitResource(e)
		resource.Row = 1
		for _, ticket := range []*relationTicket{a, b} {
			if err := e.locks.Acquire(context.Background(), ticket, resource, keyShare); err != nil {
				t.Fatal(err)
			}
		}
		first, second := pgLocksUnitAssign(t, e, a), pgLocksUnitAssign(t, e, b)
		r, _ := pgLocksUnitRequest(t, e, waiter, resource, rowUpdate, completionTransaction)
		if r == nil {
			t.Fatal("UPDATE failed to queue")
		}
		before := pgLocksUnitWait(t, e, waiter)
		if before.Kind != pgTransactionLock || before.XID != first || before.WaitStart != r.enqueuedAt {
			t.Fatal("first actual holder", before)
		}
		if again := pgLocksUnitWait(t, e, waiter); again != before {
			t.Fatal("unstable observation", before, again)
		}
		e.endLockOwner(a)
		after := pgLocksUnitWait(t, e, waiter)
		if after.XID != second || after.WaitStart != before.WaitStart || r.terminal {
			t.Fatal("multi-holder retarget/grant", after, r.terminal)
		}
		e.endLockOwner(b)
		if err := deadlockResult(t, r); err != nil {
			t.Fatal(err)
		}
		for _, r := range pgLocksUnitCapture(t, e) {
			if r.Waiting || r.Kind == pgRelationLock && r.Object == uint32(resource.Address.ObjectID) {
				t.Fatal("durable row hold became tuple/relation resource", r)
			}
		}
	})
	t.Run("queue_only_genuine_VXID_and_no_key_update_bypass", func(t *testing.T) {
		e := New()
		_, a := blockingUnitOwner(t, e)
		_, b := blockingUnitOwner(t, e)
		_, c := blockingUnitOwner(t, e)
		_, bypass := blockingUnitOwner(t, e)
		resource := blockingUnitResource(e)
		resource.Row = 1
		if err := e.locks.Acquire(context.Background(), a, resource, keyShare); err != nil {
			t.Fatal(err)
		}
		br, cancelB := pgLocksUnitRequest(t, e, b, resource, rowUpdate, completionTransaction)
		cr, _ := pgLocksUnitRequest(t, e, c, resource, keyShare, completionTransaction)
		if br == nil || cr == nil {
			t.Fatal("missing actual queue-only predecessor")
		}
		before := pgLocksUnitWait(t, e, c)
		if before.Kind != pgVirtualLock || before.Target.Session != b.Ref().Session().ID() || before.Target.Local != b.Ref().ID() || before.XID != 0 {
			t.Fatal("queue-only target fabricated/dropped", before)
		}
		if r, _ := pgLocksUnitRequest(t, e, bypass, resource, noKeyUpdate, completionTransaction); r != nil {
			t.Fatal("NO KEY UPDATE bypass was changed")
		}
		// Snapshot cannot settle; both actual queued requests remain journaled.
		if br.terminal || cr.terminal {
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
		e := New()
		_, a := blockingUnitOwner(t, e)
		_, b := blockingUnitOwner(t, e)
		_, c := blockingUnitOwner(t, e)
		resource := blockingUnitResource(e)
		// This is the manager's exact intern-identity package boundary. No SQL
		// value or hash is consulted by snapshotting or becomes a public target.
		resource.key = &uniqueLockKey{bucket: uniqueLockBucket{ObjectRef: resource.ObjectRef, hash: 17}}
		if err := e.locks.Acquire(context.Background(), a, resource, uniqueClaim); err != nil {
			t.Fatal(err)
		}
		br, _ := pgLocksUnitRequest(t, e, b, resource, uniqueClaim, completionTransaction)
		cr, _ := pgLocksUnitRequest(t, e, c, resource, uniqueClaim, completionTransaction)
		before := pgLocksUnitWait(t, e, b)
		if before.Kind != pgVirtualLock || before.Target.Session != a.Ref().Session().ID() {
			t.Fatal("unassigned unique holder", before)
		}
		xid := pgLocksUnitAssign(t, e, a)
		after := pgLocksUnitWait(t, e, b)
		if after.Kind != pgTransactionLock || after.XID != xid || after.WaitStart != before.WaitStart {
			t.Fatal("assignment transition", after)
		}
		pgLocksUnitAssign(t, e, b)
		e.endLockOwner(a)
		if err := deadlockResult(t, br); err != nil {
			t.Fatal(err)
		}
		tail := pgLocksUnitWait(t, e, c)
		if tail.XID != b.AssignedPublicXID() || cr.terminal {
			t.Fatal("unique dependency retarget", tail)
		}
		e.endLockOwner(b)
		if err := deadlockResult(t, cr); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("index_wait_target_prepares_after_enqueue", func(t *testing.T) {
		e := New()
		_, holder := blockingUnitOwner(t, e)
		_, waiter := blockingUnitOwner(t, e)
		xid := pgLocksUnitAssign(t, e, holder)
		request, _ := pgLocksUnitRequest(t, e, waiter, lockResource{completion: holder}, transactionCompletion, completionVirtual)
		if request == nil {
			t.Fatal("index completion did not enqueue")
		}
		before := pgLocksUnitWait(t, e, waiter)
		if before.Kind != pgVirtualLock || before.Target.Local != holder.Ref().ID() || before.Target.Session != holder.Ref().Session().ID() {
			t.Fatal("live index completion target", before)
		}
		e.locks.DetachPrepared(holder)
		after := pgLocksUnitWait(t, e, waiter)
		if after.Kind != pgTransactionLock || after.XID != xid || after.WaitStart != before.WaitStart || after.WaitStart != request.enqueuedAt {
			t.Fatal("prepared target did not retarget same original wait", before, after)
		}
		if request.ref.completion != holder || request.policy.CompletionPurpose != completionVirtual || request.terminal {
			t.Fatal("observation replaced or settled completion request")
		}
		records := pgLocksUnitCapture(t, e)
		if len(pgLocksUnitOwnerRecords(records, holder, pgVirtualLock)) != 0 {
			t.Fatal("prepared target retained retired VXID", records)
		}
		xids := pgLocksUnitOwnerRecords(records, holder, pgTransactionLock)
		if len(xids) != 1 || !xids[0].Owner.Prepared || xids[0].XID != xid {
			t.Fatal("prepared target lost genuine XID ownership", records)
		}
		// Real completion publication can precede atomic manager release.
		// A still-journaled prepared wait must retain its genuine target here.
		e.mu.Lock()
		e.recordCommitTimestampLocked(holder.Ref())
		e.mu.Unlock()
		if gap := pgLocksUnitWait(t, e, waiter); gap != after {
			t.Fatal("prepared publication-to-manager-end gap changed dependency", after, gap)
		}
		e.endLockOwner(holder)
		if err := deadlockResult(t, request); err != nil {
			t.Fatal(err)
		}
		for _, record := range pgLocksUnitCapture(t, e) {
			if record.Waiting {
				t.Fatal("finished prepared completion retained pending row", record)
			}
		}
	})
	for _, purpose := range []completionLockPurpose{completionVirtual, completionTransaction} {
		t.Run(map[completionLockPurpose]string{completionVirtual: "index_completion_VXID", completionTransaction: "exclusion_completion_XID"}[purpose], func(t *testing.T) {
			e := New()
			_, holder := blockingUnitOwner(t, e)
			_, waiter := blockingUnitOwner(t, e)
			xid := pgLocksUnitAssign(t, e, holder)
			r, _ := pgLocksUnitRequest(t, e, waiter, lockResource{completion: holder}, transactionCompletion, purpose)
			pending := pgLocksUnitWait(t, e, waiter)
			if purpose == completionVirtual && (pending.Kind != pgVirtualLock || pending.Target.Local != holder.Ref().ID()) || purpose == completionTransaction && (pending.Kind != pgTransactionLock || pending.XID != xid) {
				t.Fatal("caller-selected completion purpose", pending)
			}
			captured := pgLocksUnitCapture(t, e)
			if len(pgLocksUnitOwnerRecords(captured, holder, pgTransactionLock)) != 1 || len(pgLocksUnitOwnerRecords(captured, holder, pgVirtualLock)) != 1 {
				t.Fatal("lazy completion duplicated exclusive ownership", captured)
			}
			e.endLockOwner(holder)
			if err := deadlockResult(t, r); err != nil {
				t.Fatal(err)
			}
			for _, r := range pgLocksUnitCapture(t, e) {
				if r.Waiting || r.Owner.Session == waiter.Ref().Session().ID() && (r.Kind == pgTransactionLock || r.Mode == transactionCompletion) {
					t.Fatal("transient completion retained ShareLock", r)
				}
			}
		})
	}
}

func TestPGLocksSnapshotOwnership(t *testing.T) {
	t.Run("concurrent_assignment", pgLocksUnitConcurrentAssignmentCapture)
	for _, ending := range []string{"cancel", "grant", "fence"} {
		t.Run(ending, func(t *testing.T) {
			e := New()
			_, a := blockingUnitOwner(t, e)
			_, b := blockingUnitOwner(t, e)
			resource := blockingUnitResource(e)
			if err := e.locks.Acquire(context.Background(), a, resource, accessExclusive); err != nil {
				t.Fatal(err)
			}
			r, cancel := pgLocksUnitRequest(t, e, b, resource, accessExclusive, completionVirtual)
			if r == nil {
				t.Fatal("missing owned request")
			}
			before := pgLocksUnitCapture(t, e)
			saved := append([]pgLockRecord(nil), before...)
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
					e.endLockOwner(a)
				case "fence":
					e.locks.Fence(a.Ref().Epoch() + 1)
				}
			}()
			for range 32 {
				// A capture is coherent at its manager linearization point. A
				// terminal outcome after capture need not invalidate owned history.
				_, err := e.locks.PGLocks(a.Ref().Epoch())
				if err != nil && !errors.Is(err, ErrCatalogReplaced) {
					t.Error(err)
				}
			}
			wg.Wait()
			err := deadlockResult(t, r)
			if ending == "grant" && err != nil || ending == "cancel" && !errors.Is(err, context.Canceled) || ending == "fence" && !errors.Is(err, ErrCatalogReplaced) {
				t.Fatal("terminal outcome", ending, err)
			}
			epoch := a.Ref().Epoch()
			if ending == "fence" {
				epoch++
			}
			records, err := e.locks.PGLocks(epoch)
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

// The manager mutex does not exclude canonical XID assignment under Engine.mu.
// Queue genuine key dependencies before racing that first assignment with a
// capture. Extra genuine grants widen the existing capture interval without
// introducing a production callback, observer hook, timer or fake XID allocator.
// Scheduling cannot force the precise atomic-load interleaving (especially with
// one execution thread); every captured outcome must nevertheless be coherent.
func pgLocksUnitConcurrentAssignmentCapture(t *testing.T) {
	for trial := range 16 {
		t.Run(strconv.Itoa(trial), func(t *testing.T) {
			e := New()
			_, holder := blockingUnitOwner(t, e)
			resource := blockingUnitResource(e)
			resource.key = &uniqueLockKey{bucket: uniqueLockBucket{ObjectRef: resource.ObjectRef, hash: 17}}
			if err := e.locks.Acquire(context.Background(), holder, resource, uniqueClaim); err != nil {
				t.Fatal(err)
			}
			for index := range 1024 {
				granted := blockingUnitResource(e)
				granted.Address.ObjectID += ids.OID(index + 1)
				if err := e.locks.Acquire(context.Background(), holder, granted, accessShare); err != nil {
					t.Fatal(err)
				}
			}
			const waiterCount = 8
			waiters := make(map[ids.SessionID]ids.TxID, waiterCount)
			for range waiterCount {
				_, waiter := blockingUnitOwner(t, e)
				request, _ := pgLocksUnitRequest(t, e, waiter, resource, uniqueClaim, completionTransaction)
				if request == nil {
					t.Fatal("logical dependency did not enqueue")
				}
				waiters[waiter.Ref().Session().ID()] = waiter.Ref().ID()
			}
			if holder.AssignedPublicXID() != 0 {
				t.Fatal("key grant assigned an XID before the genuine assignment")
			}
			type outcome struct {
				records []pgLockRecord
				err     error
			}
			started := make(chan struct{})
			done := make(chan outcome, 1)
			joined := false
			t.Cleanup(func() {
				if !joined {
					select {
					case <-done:
					case <-time.After(5 * time.Second):
						t.Error("identity capture did not join")
					}
				}
			})
			e.locks.mu.Lock()
			go func() {
				close(started)
				records, err := e.locks.PGLocks(holder.Ref().Epoch())
				done <- outcome{records, err}
			}()
			<-started
			e.locks.mu.Unlock()
			var captured outcome
			// Admit assignment while capture actually holds manager metadata,
			// rather than relying on goroutine launch or a sleep.
			deadline := time.NewTimer(4 * time.Second)
			defer deadline.Stop()
			for !joined {
				select {
				case captured = <-done:
					joined = true
				case <-deadline.C:
					t.Fatal("capture admission deadline")
				default:
				}
				if joined {
					break
				}
				if !e.locks.mu.TryLock() {
					break
				}
				e.locks.mu.Unlock()
				runtime.Gosched()
			}
			xid := pgLocksUnitAssign(t, e, holder)
			if !joined {
				select {
				case captured = <-done:
					joined = true
				case <-deadline.C:
					t.Fatal("identity capture completion deadline")
				}
			}
			if captured.err != nil {
				t.Fatal(captured.err)
			}
			var identity pgLockRecord
			seen := make(map[ids.SessionID]bool, waiterCount)
			for _, record := range captured.records {
				local, wanted := waiters[record.Owner.Session]
				if !record.Waiting || !wanted {
					continue
				}
				if record.Owner.Local != local || seen[record.Owner.Session] {
					t.Fatal("queued owner identity/multiplicity changed", record)
				}
				if len(seen) == 0 {
					identity = record
				} else if record.Kind != identity.Kind || record.XID != identity.XID || record.Target != identity.Target {
					t.Fatal("one holder has inconsistent captured dependency identities", identity, record)
				}
				if record.Target.Session != holder.Ref().Session().ID() || record.Target.Local != holder.Ref().ID() {
					t.Fatal("logical dependency changed its actual holder", record)
				}
				seen[record.Owner.Session] = true
			}
			if len(seen) != waiterCount {
				t.Fatal("capture omitted queued logical dependencies", len(seen), waiterCount)
			}
			owners := 0
			for _, record := range captured.records {
				if record.Waiting || record.Owner.Session != holder.Ref().Session().ID() || record.Owner.Local != holder.Ref().ID() || record.Kind != identity.Kind {
					continue
				}
				if identity.Kind == pgVirtualLock && record.Target == identity.Target || identity.Kind == pgTransactionLock && record.XID == identity.XID {
					owners++
				}
			}
			if owners != 1 || identity.Kind == pgTransactionLock && identity.XID != xid {
				t.Fatal("queued dependency lacks its corresponding captured exclusive owner", identity, "matching owners", owners, "assigned XID", xid)
			}
		})
	}
}

func pgLocksUnitBagEqual(a, b []pgLockRecord) bool {
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
