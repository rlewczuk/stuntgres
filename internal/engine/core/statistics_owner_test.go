package core_test

import (
	"context"
	"errors"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/role"
	"stuntgres/internal/engine/rolecmds"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
	"time"
)

func TestStatisticsAlterDescriptorPreservation(t *testing.T) {
	for _, expression := range []bool{false, true} {
		t.Run(map[bool]string{false: "keys", true: "expression"}[expression], func(t *testing.T) {
			e, rel := statisticsUnitEngine(t)
			aclUnitRoles(e, "statistics_recipient")
			recipient := e.Roles().LookupRef("statistics_recipient")
			op, done := enginetest.RoleOperation(t, e, "postgres")
			defer done()
			syntax := statisticsUnitCreate()
			if expression {
				parsed, err := parser.Parse("CREATE STATISTICS statistics_definition ON (a+1),c FROM statistics_input")
				if err != nil {
					t.Fatal(err)
				}
				syntax = parsed[0].(*parser.CreateStatistics)
			}
			stage, err := statisticsUnitStage(t, e, op, syntax)
			if err != nil {
				t.Fatal(err)
			}
			old := statisticsUnitDescriptor(t, stage)
			extensionUnitPublish(t, e, op, stage)
			e.DiscardCatalogStage(stage)
			captured := e.CatalogRead()
			key := storage.ExtendedStatisticsKey{Definition: e.Catalog.Domain.Ref(captured.Epoch(), old.Address())}
			types := make(map[sqltype.ID]*sqltype.Descriptor)
			for _, id := range []sqltype.ID{sqltype.Int4, sqltype.PgNDistinct, sqltype.PgDependencies, sqltype.PgMCVList} {
				typ, ok := captured.LookupType(id)
				if !ok {
					t.Fatal("missing canonical collector type", id)
				}
				types[id] = typ
			}
			columns := make([]storage.StatisticsColumn, old.Selection.DimensionLen())
			for i := range columns {
				usage := types[sqltype.Int4].Usage()
				if i < old.Selection.Len() {
					usage = rel.Columns[rel.IndexForAttribute(old.Selection.Key(i))].Usage
				} else {
					usage = old.Expressions.At(i - old.Selection.Len()).Usage()
				}
				columns[i] = storage.StatisticsColumn{Key: storage.StatisticsKey{Attribute: old.Selection.Dimension(i)}, Usage: usage, Slot: i}
			}
			collector, err := storage.NewExtendedCollector(storage.ExtendedInput{Key: key, Definition: old, Columns: columns, Type: types[sqltype.PgNDistinct], DependenciesType: types[sqltype.PgDependencies], MCVType: types[sqltype.PgMCVList], MCVTarget: 100})
			if err != nil {
				t.Fatal(err)
			}
			for _, n := range []int64{1, 1, 2, 2} {
				values := make([]sqltype.Value, len(columns))
				for i := range values {
					values[i], err = sqltype.Integer(columns[i].Usage.Type, n)
					if err != nil {
						t.Fatal(err)
					}
				}
				if err = collector.Add(t.Context(), values); err != nil {
					t.Fatal(err)
				}
			}
			fact, err := collector.Finish(t.Context())
			if err != nil || fact == nil {
				t.Fatal(err)
			}
			facts := &storage.RelationStatistics{Extended: map[storage.ExtendedStatisticsKey]*storage.ExtendedStatistics{key: fact}}
			ns := &catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 50000}, ObjectName: "statistics_destination", ObjectRevision: 1, Owner: old.Owner}
			e.Mu.Lock()
			state := catalog.CloneState(e.Catalog.State)
			state.SetDescriptor(ns.Address(), ns)
			state.BindName(catalog.DescriptorNameKey(ns), ns.Address())
			e.Catalog.PublishLocked(state)
			e.Mu.Unlock()
			name := configName(old.Name())
			for _, action := range []string{"rename", "schema", "owner", "target", "default"} {
				before := e.CatalogRead()
				previous, _ := before.Statistics(old.Address())
				e.Mu.Lock()
				auth, err := e.AuthorizationLocked(op)
				if err != nil {
					e.Mu.Unlock()
					t.Fatal(err)
				}
				next := e.BeginCatalogStage(auth.Catalog)
				next.AuthorizeLocked(auth, op.Session)
				host := core.DDLHost{E: e, Op: op, CatalogStage: next}
				switch action {
				case "rename":
					err = ddl.StageRenameStatistics(host, auth, &parser.AlterStatisticsRename{Name: name, NewName: parser.Identifier{Text: "statistics_renamed"}})
					name = configName("statistics_renamed")
				case "schema":
					err = ddl.StageStatisticsNamespace(host, auth, &parser.AlterStatisticsSchema{Name: name, Schema: parser.Identifier{Text: ns.Name()}})
					name = parser.ObjectName{Parts: []parser.Identifier{{Text: ns.Name()}, {Text: "statistics_renamed"}}}
				case "owner":
					err = role.StageOwnership(next, previous, recipient)
				case "target":
					err = ddl.StageAlterStatisticsTarget(host, auth, &parser.AlterStatisticsTarget{Name: name, Target: 10000})
				case "default":
					err = ddl.StageAlterStatisticsTarget(host, auth, &parser.AlterStatisticsTarget{Name: name, Target: -1})
				}
				e.Mu.Unlock()
				if err != nil {
					t.Fatal(action, err)
				}
				changed := statisticsUnitDescriptor(t, next)
				if changed.Address() != old.Address() || changed.Relation != old.Relation || changed.Selection != old.Selection || changed.Expressions != old.Expressions || changed.Revision() <= previous.Revision() {
					t.Fatal("metadata COW replaced definition identity", action)
				}
				expected := *previous
				expected.ObjectRevision = changed.ObjectRevision
				switch action {
				case "rename":
					expected.ObjectName = "statistics_renamed"
				case "schema":
					expected.ObjectNamespace = ns.Address()
				case "owner":
					expected.Owner = recipient
				case "target":
					expected.TargetSet = true
					expected.Target = 10000
				case "default":
					expected.TargetSet = false
					expected.Target = 0
				}
				if *changed != expected {
					t.Fatal("metadata alteration changed unrelated fields", action)
				}
				extensionUnitPublish(t, e, op, next)
				e.DiscardCatalogStage(next)
				after := e.CatalogRead()
				if err = (catalog.StatisticsProvider{}).Validate(after, changed); err != nil {
					t.Fatal(err)
				}
				if after.NameAddress(catalog.DescriptorNameKey(changed)) != old.Address() {
					t.Fatal("new index missing")
				}
				if catalog.DescriptorNameKey(changed) != catalog.DescriptorNameKey(previous) && after.NameAddress(catalog.DescriptorNameKey(previous)) != (ids.ObjectAddress{}) {
					t.Fatal("old index survived")
				}
				if storage.CompatibleExtendedStatistics(facts, rel, after) != facts || facts.Extended[key] != fact {
					t.Fatal("compatible collected root replaced")
				}
				if captured.Get(old.Address()) != old || old.Expressions != changed.Expressions {
					t.Fatal("old capture mutated")
				}
			}
			e.Mu.Lock()
			auth, err := e.AuthorizationLocked(op)
			if err != nil {
				e.Mu.Unlock()
				t.Fatal(err)
			}
			noop := e.BeginCatalogStage(auth.Catalog)
			noop.AuthorizeLocked(auth, op.Session)
			current, _ := auth.Catalog.Statistics(old.Address())
			err = ddl.StageStatisticsNamespace(core.DDLHost{E: e, Op: op, CatalogStage: noop}, auth, &parser.AlterStatisticsSchema{Name: name, Schema: parser.Identifier{Text: ns.Name()}})
			if err == nil {
				err = role.StageOwnership(noop, current, current.Owner)
			}
			e.Mu.Unlock()
			if err != nil || noop.Get(current.Address()) != nil || noop.DescriptorCount() != 0 ||
				noop.RemovalCount() != 0 || noop.StageRead().Get(current.Address()) != current ||
				e.CatalogRead().Get(current.Address()) != current {
				t.Fatal("no-op replaced descriptor", err)
			}
			e.DiscardCatalogStage(noop)
		})
	}
}

func TestStatisticsAlterPublication(t *testing.T) {
	for _, mode := range []string{"cancel", "source_replaced", "namespace_replaced", "destination_collision"} {
		t.Run(mode, func(t *testing.T) {
			e, _ := statisticsUnitEngine(t)
			op, done := enginetest.RoleOperation(t, e, "postgres")
			defer done()
			initial, err := statisticsUnitStage(t, e, op, statisticsUnitCreate())
			if err != nil {
				t.Fatal(err)
			}
			old := statisticsUnitDescriptor(t, initial)
			extensionUnitPublish(t, e, op, initial)
			e.DiscardCatalogStage(initial)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			mutation, finish, err := enginetest.Operation(e, ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer finish()
			e.Mu.Lock()
			auth, err := e.AuthorizationLocked(mutation)
			if err != nil {
				e.Mu.Unlock()
				t.Fatal(err)
			}
			stage := e.BeginCatalogStage(auth.Catalog)
			stage.AuthorizeLocked(auth, mutation.Session)
			err = ddl.StageRenameStatistics(core.DDLHost{E: e, Op: mutation, CatalogStage: stage}, auth, &parser.AlterStatisticsRename{Name: configName(old.Name()), NewName: parser.Identifier{Text: "statistics_new"}})
			e.Mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			defer e.DiscardCatalogStage(stage)
			prepared, err := e.ValidateCatalogStage(ctx, stage)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "cancel" {
				cancel()
			} else {
				e.Mu.Lock()
				state := catalog.CloneState(e.Catalog.State)
				switch mode {
				case "source_replaced":
					n := *old
					n.ObjectRevision = old.Revision() + 1
					n.ObjectAddress.ObjectID += 10000
					state.RemoveDescriptor(old.Address())
					state.SetDescriptor(n.Address(), &n)
					state.BindName(catalog.DescriptorNameKey(&n), n.Address())
				case "namespace_replaced":
					ns := *stage.Base.MustNamespace(old.Namespace())
					ns.ObjectRevision = ns.Revision() + 1
					ns.ObjectAddress.ObjectID += 10000
					state.RemoveDescriptor(old.Namespace())
					state.SetDescriptor(ns.Address(), &ns)
					state.BindName(catalog.DescriptorNameKey(&ns), ns.Address())
				case "destination_collision":
					n := *old
					n.ObjectName = "statistics_new"
					n.ObjectAddress.ObjectID += 10000
					n.ObjectRevision = old.Revision() + 1
					state.SetDescriptor(n.Address(), &n)
					state.BindName(catalog.DescriptorNameKey(&n), n.Address())
				}
				e.Catalog.PublishLocked(state)
				e.Mu.Unlock()
			}
			incumbent := e.CatalogRead()
			_, err = e.PublishCatalogOperation(mutation, nil, prepared, core.PublicationToken{Epoch: stage.Base.Epoch()})
			if err == nil || e.CatalogRead().State() != incumbent.State() {
				t.Fatal("rejected mutation published", mode, err)
			}
			if mode == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if stage.Base.Get(old.Address()) != old {
				t.Fatal("retained definition changed")
			}
		})
	}
}

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
			extensionUnitPublish(t, e, setup, initial)
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
