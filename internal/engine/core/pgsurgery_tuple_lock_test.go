package core_test

import (
	"context"
	"runtime"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

func freezeTupleWait(t *testing.T, e *core.Engine, ticket *lock.Ticket, resource lock.Resource, done <-chan error) *lock.Request {
	t.Helper()
	guard := time.NewTimer(enginetest.Liveness)
	defer guard.Stop()
	for {
		state := e.Locks.Inspect(ticket).Resources[resource]
		if state != nil {
			for _, pending := range state.Pending {
				if pending.Ticket == ticket && !pending.Terminal {
					return pending.Request
				}
			}
		}
		select {
		case err := <-done:
			t.Fatalf("worker completed before the actual request queued on %v: %v", resource, err)
		case <-guard.C:
			t.Fatal("actual tuple request never queued")
		default:
			runtime.Gosched()
		}
	}
}

func freezeTupleJoin(t *testing.T, done <-chan error) {
	t.Helper()
	guard := time.NewTimer(enginetest.Liveness)
	defer guard.Stop()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-guard.C:
		t.Fatal("tuple admission failed to finish")
	}
}

func freezeTupleRepair(t *testing.T, e *core.Engine, store *storage.Relation, row storage.RowVersion, ref ids.ObjectRef) lock.Resource {
	t.Helper()
	e.Mu.Lock()
	headers, err := storage.RepairHeapHeaders(store.Headers, []storage.RowVersion{row})
	if err == nil {
		store.Headers = headers
		store.Generation++
	}
	resource, keyErr := exec.TupleResource(ref, row, headers)
	e.Mu.Unlock()
	if err != nil || keyErr != nil {
		t.Fatal(err, keyErr)
	}
	return resource
}

// Real transaction tickets and actual queued requests distinguish both races:
// repair while a tuple wait is pending, and repair after grant before Stage.
// No callback, sleep, mocked grant or RowID-only surrogate drives acceptance.
func TestPgsurgeryFreezeTupleReadmission(t *testing.T) {
	for _, phase := range []string{"after_wait", "stage"} {
		t.Run(phase, func(t *testing.T) {
			e := engine.New()
			session := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, session, "CREATE TABLE t(v INT)")
			enginetest.SessionSQL(t, session, "INSERT INTO t VALUES(1),(2)")
			scope, binding, source := deleteProbeScope(t, session)
			h := scope.WriteHost()
			ref, store := binding.Ref, binding.Storage
			row, sentinel := source.Committed[0], source.Committed[1]
			ordinary := lock.Resource{ObjectRef: ref, Row: row.Row}
			unrelated := lock.Resource{ObjectRef: ref, Row: sentinel.Row}
			_, original := blockingUnitOwner(t, e)
			_, waiting := blockingUnitOwner(t, e)
			_, repaired := blockingUnitOwner(t, e)
			var done chan error
			t.Cleanup(func() {
				e.EndLockOwner(repaired)
				e.EndLockOwner(original)
				e.EndLockOwner(waiting)
				e.EndLockOwner(scope.LockTicket)
				if done != nil {
					freezeTupleJoin(t, done)
				}
			})
			if err := e.Locks.Acquire(context.Background(), original, unrelated, lock.ForUpdate); err != nil {
				t.Fatal(err)
			}
			pending := deadlockRequest(t, e, waiting, unrelated, lock.ForUpdate)
			if pending == nil {
				t.Fatal("sentinel request did not genuinely wait")
			}
			if phase == "after_wait" {
				if err := e.Locks.Acquire(context.Background(), original, ordinary, lock.ForUpdate); err != nil {
					t.Fatal(err)
				}
				done = make(chan error, 1)
				go func() { done <- h.AcquireUpdateRow(ref, row, lock.ForNoKeyUpdate); close(done) }()
				freezeTupleWait(t, e, scope.LockTicket, ordinary, done)
			} else {
				if err := h.AcquireUpdateRow(ref, row, lock.ForNoKeyUpdate); err != nil {
					t.Fatal(err)
				}
			}
			key := freezeTupleRepair(t, e, store, row, ref)
			if err := e.Locks.Acquire(context.Background(), repaired, key, lock.ForUpdate); err != nil {
				t.Fatal(err)
			}
			state := e.Locks.Inspect(original, waiting)
			if !e.Locks.Holds(original, unrelated, lock.ForUpdate) || state.Resources[unrelated] == nil || len(state.Resources[unrelated].Pending) != 1 || state.Resources[unrelated].Pending[0].Request != pending || pending.Terminal() {
				t.Fatal("repair changed unrelated actual grant/request ownership")
			}
			if phase == "after_wait" {
				e.EndLockOwner(original)
				if err := deadlockResult(t, pending); err != nil {
					t.Fatal(err)
				}
			} else {
				done = make(chan error, 1)
				go func() {
					batch := index.WriteBatch{Values: [][]sqltype.Value{row.Values}, ActionMode: lock.MultiNoKeyAction}
					stale, err := h.StageReplacement(ref, []storage.RowVersion{row}, batch, nil, dml.ReplaceAppend)
					if (stale.IndexStale || stale.RecheckIndex >= 0) && err == nil {
						err = context.Canceled
					}
					done <- err
					close(done)
				}()
			}
			freezeTupleWait(t, e, scope.LockTicket, key, done)
			if !e.Locks.Holds(scope.LockTicket, ordinary, lock.ForNoKeyUpdate) {
				t.Fatal("readmission discarded its genuine earlier resource grant")
			}
			if phase == "stage" {
				if len(scope.Stage.Rows[ref.Address]) != 0 || pending.Terminal() {
					t.Fatal("Stage accepted before readmission or released unrelated waiter")
				}
			}
			e.EndLockOwner(repaired)
			freezeTupleJoin(t, done)
			if !e.Locks.Holds(scope.LockTicket, key, lock.ForNoKeyUpdate) {
				t.Fatal("current exact key was never admitted")
			}
			if phase == "stage" {
				staged := scope.Stage.Rows[ref.Address]
				if len(staged) != 1 || staged[0].Version == row.Version || staged[0].PrivateHistory.Version.RepairStamp != store.Headers.Header(row.Version).RepairStamp {
					t.Fatal("Stage captured the wrong actual predecessor header")
				}
				inherited, err := exec.TupleResource(ref, staged[0], store.Headers)
				if err != nil || inherited != key {
					t.Fatal("actual successor lost its repair lineage", inherited, key, err)
				}
				facts := []lock.TupleMaintenance{{Tuple: lock.MultiTuple{Resource: key, Version: row.Version}}, {Tuple: lock.MultiTuple{Resource: ordinary, Version: row.Version}}}
				if err := e.Locks.CaptureTupleMaintenance(context.Background(), scope.LockTicket, facts); err != nil {
					t.Fatal(err)
				}
				if !facts[0].Present || facts[0].Xmax != scope.LockTicket.AssignedPublicXID() || facts[1].Present {
					t.Fatal("physical action was attributed to a rederived/old resource", facts)
				}
				e.EndLockOwner(original)
				if err := deadlockResult(t, pending); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestPgsurgeryFreezeTupleProjection(t *testing.T) {
	e := engine.New()
	session := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, session, "CREATE TABLE t(v INT)")
	enginetest.SessionSQL(t, session, "INSERT INTO t VALUES(1)")
	scope, binding, source := deleteProbeScope(t, session)
	h := scope.WriteHost()
	row := source.Committed[0]
	leaf := exec.Leaf{Ref: binding.Ref, Relation: binding.Relation, Storage: binding.Storage}
	mark := plan.RowMark{Mode: lock.ForUpdate}
	if ok, err := h.AcquireQueryRow(leaf, row, mark); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if _, live, err := h.LatestQueryRow(leaf, row, 0, mark); err != nil || !live {
		t.Fatal(live, err)
	}
	before, err := h.CaptureTupleXmax(binding.Ref, []storage.RowVersion{row})
	if err != nil || len(before) != 1 || before[0] != scope.LockTicket.AssignedPublicXID() || before[0] == 0 {
		t.Fatal("missing raw lock-only XID", before, err)
	}
	freezeTupleRepair(t, e, binding.Storage, row, binding.Ref)
	// Completing an effect for the grant admitted before repair must not
	// discover the new header key and become a genuinely later owner.
	if err := h.PhysicalRowLock(binding.Ref, row, lock.ForUpdate); err != nil {
		t.Fatal(err)
	}
	after, err := h.CaptureTupleXmax(binding.Ref, []storage.RowVersion{row})
	if err != nil || after[0] != 0 || before[0] == 0 || row.DeleteFullXID != 0 || source.Committed[0].DeleteFullXID != 0 {
		t.Fatal("raw projection mutated end provenance/old capture", before, after, row, err)
	}
	xid := enginetest.Usage(e, sqltype.Xid)
	n := plan.SystemAttributeExpr{Info: expr.Info{Usage: xid}, Owner: binding.Ref, Scope: 1, Frame: 1, SubID: -4}
	frame := exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1, PhysicalTable: binding.Ref, PhysicalVersion: row, PhysicalRawXmax: map[ids.VersionID]uint32{row.Version: before[0]}}}
	value, err := n.Eval(&frame)
	got, xidErr := value.XID()
	if err != nil || xidErr != nil || got != before[0] {
		t.Fatal("pure old frame did not retain detached scalar", got, err, xidErr)
	}
	if !e.Locks.Holds(scope.LockTicket, lock.Resource{ObjectRef: binding.Ref, Row: row.Row}, lock.ForUpdate) {
		t.Fatal("presentation/repair retired old ticket")
	}
}
