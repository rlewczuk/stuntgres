package lock_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
)

type databaseLockOwner struct{ ids.TxOwnerMarker }

func TestDatabaseAlterSharedLocks(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	a, b, shared := new(ids.Domain), new(ids.Domain), new(ids.Domain)
	manager := lock.New(a)
	if err := manager.AdmitDomain(b); err != nil {
		t.Fatal(err)
	}
	if err := manager.SetRoleDomain(shared, 1); err != nil {
		t.Fatal(err)
	}
	transaction := func(domain *ids.Domain, id ids.SessionID) *lock.Ticket {
		ticket, err := manager.Register(domain.Tx(domain.Session(id), ids.TxID(id), 1, &databaseLockOwner{}, 1))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { manager.End(ticket) })
		return ticket
	}
	first, second := transaction(a, 1), transaction(b, 2)
	object := lock.Resource{ObjectRef: shared.Ref(1, ids.ObjectAddress{ClassID: ids.DatabaseClassID, ObjectID: 17000})}
	for _, invalid := range []lock.Resource{
		{ObjectRef: a.Ref(1, object.Address)},
		{ObjectRef: shared.Ref(2, object.Address)},
		{ObjectRef: shared.Ref(1, ids.ObjectAddress{ClassID: ids.DatabaseClassID, ObjectID: 17000, SubID: 1})},
	} {
		if _, err := manager.StartAcquire(ctx, first, invalid, lock.AccessExclusive, lock.Policy{}); err == nil {
			t.Fatal("foreign database resource accepted", invalid)
		}
	}
	checkpoint := manager.Checkpoint(first)
	if err := manager.Acquire(ctx, first, object, lock.AccessExclusive); err != nil {
		t.Fatal(err)
	}
	request, err := manager.StartAcquire(ctx, second, object, lock.AccessExclusive, lock.Policy{})
	if err != nil || request == nil {
		t.Fatal("shared identity did not exclude peer", err)
	}
	manager.Restore(first, checkpoint)
	if err := request.Wait(); err != nil {
		t.Fatal(err)
	}
	rows, err := manager.PGLocks(b, 1)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range rows {
		if row.Kind == lock.ObjectRecord && row.Class == uint32(ids.DatabaseClassID) && row.Object == 17000 && row.Shared {
			found = true
		}
	}
	if !found {
		t.Fatal("shared database grant missing from pg_locks", rows)
	}
	manager.End(second)
	name := lock.Resource{ObjectRef: shared.Ref(1, ids.ObjectAddress{}), Name: ids.NameKey{ClassID: ids.DatabaseClassID, Name: "Exact Database"}}
	if err := manager.Acquire(ctx, first, name, lock.AccessExclusive); err != nil {
		t.Fatal(err)
	}
	persistent, err := manager.RegisterSession(b.Session(3))
	if err != nil {
		t.Fatal(err)
	}
	defer manager.End(persistent)
	if err := manager.Acquire(ctx, persistent, object, lock.RowExclusive); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.StartAcquire(ctx, persistent, name, lock.AccessExclusive, lock.Policy{}); err == nil {
		t.Fatal("persistent destination claim accepted")
	}
	role := object
	role.ObjectRef = shared.Ref(1, ids.ObjectAddress{ClassID: ids.RoleClassID, ObjectID: 10})
	if _, err := manager.StartAcquire(ctx, persistent, role, lock.AccessShare, lock.Policy{}); err == nil {
		t.Fatal("persistent role resource accepted")
	}
	waiting, err := manager.StartAcquire(ctx, first, object, lock.AccessExclusive, lock.Policy{})
	if err != nil || waiting == nil {
		t.Fatal("startup grant failed to exclude rename", err)
	}
	manager.ReleaseDatabaseAdmission(persistent, object)
	if err := waiting.Wait(); err != nil {
		t.Fatal(err)
	}
	manager.DetachPrepared(first)
	third := transaction(b, 4)
	queued, err := manager.StartAcquire(ctx, third, object, lock.AccessExclusive, lock.Policy{})
	if err != nil || queued == nil {
		t.Fatal("prepared grant was released", err)
	}
	manager.End(first)
	if err := queued.Wait(); err != nil {
		t.Fatal(err)
	}
	// A persistent startup request must wait without a fabricated transaction
	// provenance, then be released independently of physical session lifetime.
	startup, err := manager.RegisterSession(a.Session(5))
	if err != nil {
		t.Fatal(err)
	}
	defer manager.End(startup)
	pending, err := manager.StartAcquire(ctx, startup, object, lock.RowExclusive, lock.Policy{})
	if err != nil || pending == nil {
		t.Fatal("startup request did not queue", err)
	}
	rows, err = manager.PGLocks(a, 1)
	if err != nil {
		t.Fatal(err)
	}
	found = false
	for _, row := range rows {
		if row.Waiting && row.Kind == lock.ObjectRecord && row.Shared && row.Class == uint32(ids.DatabaseClassID) && row.Object == 17000 && row.Mode == lock.RowExclusive {
			found = true
		}
	}
	if !found {
		t.Fatal("queued startup grant missing from pg_locks", rows)
	}
	manager.End(third)
	if err := pending.Wait(); err != nil {
		t.Fatal(err)
	}
	manager.ReleaseDatabaseAdmission(startup, object)
	// Concurrent source copies share the genuine Share mode, but physical startup
	// is a RowExclusive writer. Metadata-only ACL AS remains compatible with both.
	left, right := transaction(a, 6), transaction(b, 7)
	if err := manager.Acquire(ctx, left, object, lock.Share); err != nil {
		t.Fatal(err)
	}
	if err := manager.Acquire(ctx, right, object, lock.Share); err != nil {
		t.Fatal("compatible source copies excluded one another", err)
	}
	acl := transaction(a, 8)
	if err := manager.Acquire(ctx, acl, object, lock.AccessShare); err != nil {
		t.Fatal("metadata grant conflicted with source copy", err)
	}
	blocked, err := manager.StartAcquire(ctx, startup, object, lock.RowExclusive, lock.Policy{})
	if err != nil || blocked == nil {
		t.Fatal("source copy did not exclude startup", err)
	}
	manager.End(left)
	select {
	case <-blocked.Done():
		t.Fatal("startup crossed remaining source copy")
	default:
	}
	manager.End(right)
	if err := blocked.Wait(); err != nil {
		t.Fatal(err)
	}
	manager.ReleaseDatabaseAdmission(startup, object)
	if _, err := manager.StartAcquire(ctx, startup, object, lock.AccessShare, lock.Policy{}); err == nil {
		t.Fatal("persistent ordinary AS accepted outside startup mode")
	}
}

func TestDatabaseAlterStartupBlockingPids(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, cancelWait := range []bool{false, true} {
			name := map[bool]string{false: "live", true: "prepared"}[prepared] + map[bool]string{false: "/grant", true: "/cancel"}[cancelWait]
			t.Run(name, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
				defer cancel()
				a, b, shared := new(ids.Domain), new(ids.Domain), new(ids.Domain)
				manager := lock.New(a)
				if err := manager.AdmitDomain(b); err != nil {
					t.Fatal(err)
				}
				if err := manager.SetRoleDomain(shared, 1); err != nil {
					t.Fatal(err)
				}
				holder, err := manager.Register(a.Tx(a.Session(11), 1, 1, &databaseLockOwner{}, 1))
				if err != nil {
					t.Fatal(err)
				}
				defer manager.End(holder)
				holder.MirrorFullXID(42)
				object := lock.Resource{ObjectRef: shared.Ref(1, ids.ObjectAddress{ClassID: ids.DatabaseClassID, ObjectID: 17000})}
				if err := manager.Acquire(ctx, holder, object, lock.AccessExclusive); err != nil {
					t.Fatal(err)
				}
				wantPID := int32(11)
				if prepared {
					manager.DetachPrepared(holder)
					wantPID = 0
				}
				startup, err := manager.RegisterSession(b.Session(22))
				if err != nil {
					t.Fatal(err)
				}
				defer manager.End(startup)
				waitCtx, stop := context.WithCancel(ctx)
				defer stop()
				request, err := manager.StartAcquire(waitCtx, startup, object, lock.RowExclusive, lock.Policy{})
				if err != nil || request == nil {
					t.Fatal("startup did not queue behind shared database holder", request, err)
				}
				for _, observer := range []*ids.Domain{a, b} {
					pids, err := manager.BlockingPids(observer, 22)
					if err != nil || len(pids) != 1 || pids[0] != wantPID {
						t.Fatal("startup blocker discovery", observer == a, pids, err)
					}
					records, err := manager.PGLocks(observer, 1)
					if err != nil {
						t.Fatal(err)
					}
					grants, waits := 0, 0
					for _, record := range records {
						if record.Kind != lock.ObjectRecord || record.Class != uint32(ids.DatabaseClassID) || record.Object != 17000 {
							continue
						}
						if !record.Shared || record.Subobject != 0 {
							t.Fatal("startup object tag lost shared authority", record)
						}
						if record.Waiting {
							if record.Owner != (lock.RecordOwner{Session: 22}) || record.Mode != lock.RowExclusive || record.WaitStart.IsZero() {
								t.Fatal("startup lock and inquiry disagree", record)
							}
							waits++
						} else {
							if record.Owner != (lock.RecordOwner{Session: 11, Local: 1, Prepared: prepared}) || record.Mode != lock.AccessExclusive {
								t.Fatal("holder lock and inquiry disagree", record)
							}
							grants++
						}
					}
					if grants != 1 || waits != 1 {
						t.Fatal("missing actual startup grant/wait pair", grants, waits)
					}
				}
				if cancelWait {
					stop()
					if err := request.Wait(); !errors.Is(err, context.Canceled) || manager.Holds(startup, object, lock.RowExclusive) {
						t.Fatal("canceled startup retained grant", err)
					}
				} else {
					manager.End(holder)
					if err := request.Wait(); err != nil || !manager.Holds(startup, object, lock.RowExclusive) {
						t.Fatal("startup did not receive actual grant", err)
					}
					manager.ReleaseDatabaseAdmission(startup, object)
				}
				for _, observer := range []*ids.Domain{a, b} {
					pids, err := manager.BlockingPids(observer, 22)
					if err != nil || len(pids) != 0 {
						t.Fatal("terminal startup still reported blockers", pids, err)
					}
					records, err := manager.PGLocks(observer, 1)
					if err != nil {
						t.Fatal(err)
					}
					for _, record := range records {
						if record.Owner.Session == 22 {
							t.Fatal("terminal startup retained lock record", record)
						}
					}
				}
			})
		}
	}
}
