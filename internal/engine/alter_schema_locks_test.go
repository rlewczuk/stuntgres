package engine

import (
	"context"
	"testing"
	"time"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
)

func TestAlterSchemaLockCancellationAndTimeout(t *testing.T) {
	for _, target := range []string{"t", "q2"} {
		for _, timeout := range []bool{false, true} {
			t.Run(target+map[bool]string{false: "/cancel", true: "/timeout"}[timeout], func(t *testing.T) {
				e := New()
				alterSchemaEngineFixture(t, e)
				a, b := sessionStart(t, e, nil), sessionStart(t, e, nil)
				source := e.catalog.state.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "s1"})
				original := make(map[ids.ObjectAddress]catalog.Descriptor)
				for _, name := range []string{"t", "q1", "q2"} {
					address := NameAddressForTempMove(t, e, source, name)
					original[address] = e.catalog.state.Get(address)
				}
				address := NameAddressForTempMove(t, e, source, target)
				ref := e.catalog.Domain.Ref(e.catalog.state.Epoch(), address)
				tx := transactionBegin(t, a)
				if err := e.locks.Acquire(context.Background(), a.transactions.ticket, lock.Resource{ObjectRef: ref}, lock.AccessExclusive); err != nil {
					t.Fatal(err)
				}
				q := "ALTER TABLE s1.t SET SCHEMA s2"
				lockAwait(t, waitsCommandAsync(e, b, context.Background(), q, true), nil)
				lockPending(t, e, 0)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				if timeout {
					sessionSQL(t, b, "SET lock_timeout='80ms'")
				}
				done := waitsCommandAsync(e, b, ctx, q, false)
				lockPending(t, e, 1)
				if timeout {
					select {
					case err := <-done:
						renameWantError(t, err, "55P03", "canceling statement due to lock timeout")
					case <-time.After(5 * time.Second):
						t.Fatal("SET SCHEMA timeout stuck")
					}
				} else {
					cancel()
					lockAwait(t, done, context.Canceled)
				}
				for address, old := range original {
					if e.catalog.state.Get(address) != old {
						t.Fatal("partial closure move", address)
					}
				}
				lockPending(t, e, 0)
				transactionEnd(t, a, tx, false)
				sessionSQL(t, b, "RESET lock_timeout")
				lockAwait(t, waitsCommandAsync(e, b, context.Background(), q, false), nil)
				lockEmpty(t, e)
			})
		}
	}
}
