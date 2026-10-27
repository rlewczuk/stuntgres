package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
)

func TestRoleSettingsScopeLocks(t *testing.T) {
	for _, cancelWait := range []bool{false, true} {
		t.Run(map[bool]string{false: "latest_predecessor", true: "cancellation"}[cancelWait], func(t *testing.T) {
			e := engine.New()
			holder, holderDone := enginetest.RoleOperation(t, e, "postgres")
			defer holderDone()
			holderTx, err := e.Begin(holder, session.TxOptions{})
			if err != nil {
				t.Fatal(err)
			}
			holderActive := true
			defer func() {
				if holderActive {
					_, _ = e.Rollback(holder, holderTx)
				}
			}()
			holderScope := &core.StatementScope{Op: holder, Owner: holder.Session.Transactions, TxRef: holderTx, LockTicket: holder.Session.Transactions.Ticket}
			principal := e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
			key := access.RoleSettingKey{Role: principal}
			other := access.RoleSettingKey{Role: principal, Database: e.Catalog.DatabaseAddress()}
			if err := holderScope.AcquireRoleSetting(key); err != nil {
				t.Fatal(err)
			}
			e.Mu.Lock()
			base := e.OperationRolesLocked(holder)
			candidate := base.WithSettings(base.Settings.WithRow(key, []access.RoleSetting{{Name: "application_name", Value: "winner"}}))
			err = core.InstallOperationRolesLocked(e, holder, candidate)
			e.Mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			waiter, waiterDone, err := enginetest.Operation(e, ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer waiterDone()
			waiterTx, err := e.Begin(waiter, session.TxOptions{})
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if waiter.Context().Err() != nil {
					_, _ = e.Commit(waiter, waiterTx)
				} else {
					_, _ = e.Rollback(waiter, waiterTx)
				}
			}()
			waiterScope := &core.StatementScope{Op: waiter, Owner: waiter.Session.Transactions, TxRef: waiterTx, LockTicket: waiter.Session.Transactions.Ticket}
			// Different scopes must remain independently writable even for one role.
			if err := waiterScope.AcquireRoleSetting(other); err != nil {
				t.Fatal(err)
			}
			e.Mu.Lock()
			before := e.OperationRolesLocked(waiter)
			private := before.WithSettings(before.Settings.WithRow(other, []access.RoleSetting{{Name: "application_name", Value: "private"}}))
			err = core.InstallOperationRolesLocked(e, waiter, private)
			e.Mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			result := make(chan error, 1)
			joined := false
			defer func() {
				if !joined {
					cancel()
					enginetest.LockAwait(t, result, context.Canceled)
				}
			}()
			go func() { result <- waiterScope.AcquireRoleSetting(key) }()
			enginetest.LockPending(t, e, 1)
			seen := 0
			for resource := range e.Locks.Inspect().Resources {
				if resource.Kind == lock.RoleSettingResource {
					if resource.Domain() != &e.Cluster.RoleDomain || resource.High != uint32(principal.ID()) {
						t.Fatal("scope writer used database-local or foreign authority")
					}
					seen++
				}
			}
			if seen != 2 {
				t.Fatal("distinct scope resources not retained", seen)
			}
			if cancelWait {
				cancel()
				enginetest.LockAwait(t, result, context.Canceled)
				joined = true
				if e.RoleStore.Published != base || private.Settings.Lookup(other)[0].Value != "private" {
					t.Fatal("canceled wait published or mutated private state")
				}
			} else {
				outcome, err := e.Commit(holder, holderTx)
				holderActive = false
				if err != nil || !outcome.Committed {
					t.Fatal("holder publication", err)
				}
				enginetest.LockAwait(t, result, nil)
				joined = true
				e.Mu.Lock()
				refreshed := e.OperationRolesLocked(waiter)
				e.Mu.Unlock()
				if refreshed.Settings.Lookup(key)[0].Value != "winner" || refreshed.Settings.Lookup(other)[0].Value != "private" || private.Settings.Lookup(key) != nil {
					t.Fatal("wait did not refresh latest predecessor without mutating retained root")
				}
			}
		})
	}
}

func TestRoleSettingsRetirementLock(t *testing.T) {
	e := engine.New()
	holder, holderDone := enginetest.RoleOperation(t, e, "postgres")
	defer holderDone()
	holderTx, err := e.Begin(holder, session.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	holderScope := &core.StatementScope{Op: holder, Owner: holder.Session.Transactions, TxRef: holderTx, LockTicket: holder.Session.Transactions.Ticket}
	principal := e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
	if err := holderScope.AcquireRoleSetting(access.RoleSettingKey{Role: principal}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	waiter, done, err := enginetest.Operation(e, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	tx, err := e.Begin(waiter, session.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	scope := &core.StatementScope{Op: waiter, Owner: waiter.Session.Transactions, TxRef: tx, LockTicket: waiter.Session.Transactions.Ticket}
	result := make(chan error, 1)
	go func() { result <- scope.AcquireRolePrincipal(principal, lock.AccessExclusive, false) }()
	enginetest.LockPending(t, e, 1)
	cancel()
	enginetest.LockAwait(t, result, context.Canceled)
	if _, err := e.Commit(waiter, tx); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled retirement cleanup", err)
	}
	if _, err := e.Rollback(holder, holderTx); err != nil {
		t.Fatal(err)
	}
	enginetest.LockEmpty(t, e)
}
