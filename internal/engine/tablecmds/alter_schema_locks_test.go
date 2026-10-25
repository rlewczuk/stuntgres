package tablecmds_test

import (
	"context"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
)

func TestAlterSchemaLockCancellationAndTimeout(t *testing.T) {
	for _, target := range []string{"t", "q2"} {
		for _, timeout := range []bool{false, true} {
			t.Run(target+map[bool]string{false: "/cancel", true: "/timeout"}[timeout], func(t *testing.T) {
				e := engine.New()
				alterSchemaEngineFixture(t, e)
				a, b := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
				source := e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "s1"})
				original := make(map[ids.ObjectAddress]catalog.Descriptor)
				for _, name := range []string{"t", "q1", "q2"} {
					address := NameAddressForTempMove(t, e, source, name)
					original[address] = e.Catalog.State.Get(address)
				}
				address := NameAddressForTempMove(t, e, source, target)
				ref := e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), address)
				tx := enginetest.TransactionBegin(t, a)
				if err := e.Locks.Acquire(context.Background(), a.Transactions.Ticket, lock.Resource{ObjectRef: ref}, lock.AccessExclusive); err != nil {
					t.Fatal(err)
				}
				q := "ALTER TABLE s1.t SET SCHEMA s2"
				enginetest.LockAwait(t, waitsCommandAsync(e, b, context.Background(), q, true), nil)
				enginetest.LockPending(t, e, 0)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				if timeout {
					enginetest.SessionSQL(t, b, "SET lock_timeout='80ms'")
				}
				done := waitsCommandAsync(e, b, ctx, q, false)
				enginetest.LockPending(t, e, 1)
				if timeout {
					select {
					case err := <-done:
						renameWantError(t, err, "55P03", "canceling statement due to lock timeout")
					case <-time.After(5 * time.Second):
						t.Fatal("SET SCHEMA timeout stuck")
					}
				} else {
					cancel()
					enginetest.LockAwait(t, done, context.Canceled)
				}
				for address, old := range original {
					if e.Catalog.State.Get(address) != old {
						t.Fatal("partial closure move", address)
					}
				}
				enginetest.LockPending(t, e, 0)
				enginetest.TransactionEnd(t, a, tx, false)
				enginetest.SessionSQL(t, b, "RESET lock_timeout")
				enginetest.LockAwait(t, waitsCommandAsync(e, b, context.Background(), q, false), nil)
				enginetest.LockEmpty(t, e)
			})
		}
	}
}
