package core_test

import (
	"context"
	"testing"
	"time"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/rolecmds"
	"stuntgres/internal/parser"
)

func TestStatisticsAlterOwnershipAdmission(t *testing.T) {
	for _, mode := range []string{"current", "same_owner", "same_owner_recreated", "source_recreated", "namespace_recreated", "recipient_recreated", "authority_revoked", "epoch_replaced", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			e, _ := statisticsUnitEngine(t)
			aclUnitRoles(e, "statistics_recipient")
			recipient := e.Roles().LookupRef("statistics_recipient")
			setup, setupDone := enginetest.RoleOperation(t, e, "postgres")
			initial, err := statisticsUnitStage(t, e, setup, statisticsUnitCreate())
			if err != nil {
				t.Fatal(err)
			}
			old := statisticsUnitDescriptor(t, initial)
			enginetest.PublishOperationStage(t, e, setup, initial)
			e.DiscardCatalogStage(initial)
			setupDone()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			op, done, err := enginetest.Operation(e, ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer done()
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			statement := &parser.AlterOwner{Kind: "statistics", Name: configName(old.Name()), Target: parser.RoleSpec{Name: "statistics_recipient"}}
			if mode == "same_owner" || mode == "same_owner_recreated" {
				statement.Target = parser.RoleSpec{Identity: "current"}
			}
			// Gate owns beginning the implicit incarnation and registering its
			// real ticket; merely projecting an idle owner's fields does neither.
			if err = scope.Gate(statement); err != nil {
				scope.Finish(err)
				t.Fatal(err)
			}
			finished := false
			defer func() {
				if !finished {
					scope.Finish(context.Canceled)
				}
			}()
			_, holder := blockingUnitOwner(t, e)
			resource := lock.Resource{ObjectRef: e.Catalog.Domain.Ref(e.CatalogRead().Epoch(), old.Address())}
			if err = e.Locks.Acquire(t.Context(), holder, resource, lock.AccessExclusive); err != nil {
				t.Fatal(err)
			}
			result := make(chan error, 1)
			joined := false
			go func() {
				_, err := rolecmds.ExecuteOwnership(e, scope, statement, 0)
				result <- err
			}()
			defer func() {
				cancel()
				e.EndLockOwner(holder)
				if !joined {
					<-result
				}
			}()
			deadline := time.NewTimer(5 * time.Second)
			defer deadline.Stop()
			poll := time.NewTicker(time.Millisecond)
			defer poll.Stop()
			for {
				state := e.Locks.Inspect().Resources[resource]
				if state != nil && len(state.Pending) == 1 && state.Pending[0].Ticket == scope.LockTicket &&
					state.Pending[0].Mode == lock.AccessExclusive && !state.Pending[0].Terminal &&
					state.Holders[holder].Mode == lock.AccessExclusive {
					break
				}
				select {
				case early := <-result:
					joined = true
					t.Fatal("ownership ended before original-definition lock admission", early)
				case <-deadline.C:
					t.Fatal("ownership did not queue on its original definition")
				case <-poll.C:
				}
			}
			e.Mu.Lock()
			state := catalog.CloneState(e.Catalog.State)
			switch mode {
			case "source_recreated", "same_owner_recreated":
				n := *old
				n.ObjectAddress.ObjectID += 10000
				n.ObjectRevision = old.Revision() + 1
				state.RemoveDescriptor(old.Address())
				state.SetDescriptor(n.Address(), &n)
				state.BindName(catalog.DescriptorNameKey(&n), n.Address())
				e.Catalog.PublishLocked(state)
			case "namespace_recreated":
				ns := *e.Catalog.Read(e.Catalog.State).MustNamespace(old.Namespace())
				ns.ObjectAddress.ObjectID += 10000
				ns.ObjectRevision = ns.Revision() + 1
				state.RemoveDescriptor(old.Namespace())
				state.SetDescriptor(ns.Address(), &ns)
				state.BindName(catalog.DescriptorNameKey(&ns), ns.Address())
				e.Catalog.PublishLocked(state)
			case "recipient_recreated":
				roles := cloneRoleFixture(e.RoleStore.Published)
				d := fixtureRole(roles, recipient)
				retireFixtureRole(roles, recipient)
				d.Ref = e.RoleStore.Domain.Principal(recipient.ID() + 10000)
				putFixtureRole(roles, d)
				e.RoleStore.PublishLocked(roles)
			case "epoch_replaced":
				state.SetEpoch(state.Epoch() + 1)
				e.Catalog.PublishLocked(state)
			case "authority_revoked":
				roles := cloneRoleFixture(e.RoleStore.Published)
				actor := fixtureRole(roles, old.Owner)
				actor.Superuser = false
				putFixtureRole(roles, actor)
				e.RoleStore.PublishLocked(roles)
			}
			e.Mu.Unlock()
			before := e.CatalogRead()
			if mode == "cancel" {
				cancel()
			}
			e.EndLockOwner(holder)
			err = <-result
			joined = true
			if mode == "current" || mode == "same_owner" {
				if err != nil {
					t.Fatal(err)
				}
				if err = scope.Finish(nil); err != nil {
					t.Fatal(err)
				}
				finished = true
				changed, ok := e.CatalogRead().Statistics(old.Address())
				if mode == "same_owner" {
					if !ok || changed != old || changed.Revision() != old.Revision() {
						t.Fatal("same owner changed descriptor")
					}
				} else if !ok || changed.Owner != recipient || changed.Expressions != old.Expressions || changed.Selection != old.Selection || changed.Revision() <= old.Revision() {
					t.Fatal("ordinary ownership did not preserve definition")
				}
			} else {
				if err == nil {
					t.Fatal("stale original authority accepted", mode)
				}
				if finishErr := scope.Finish(err); finishErr == nil {
					t.Fatal("failed statement lost error")
				}
				finished = true
				if e.CatalogRead().State() != before.State() {
					t.Fatal("failed admission published")
				}
			}
		})
	}
}
