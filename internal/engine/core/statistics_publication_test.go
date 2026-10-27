package core_test

import (
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/parser"
)

func TestStatisticsObjectPublicationNamespaceRetirement(t *testing.T) {
	for _, recreate := range []bool{false, true} {
		t.Run(map[bool]string{false: "retired", true: "recreated"}[recreate], func(t *testing.T) {
			e, _ := statisticsUnitEngine(t)
			op, done := enginetest.RoleOperation(t, e, "postgres")
			defer done()
			e.Mu.Lock()
			ns := &catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 50000}, ObjectName: "statistics_lifetime", ObjectRevision: 1, Owner: op.RoleState().CurrentUser}
			next := catalog.CloneState(e.Catalog.State)
			next.SetDescriptor(ns.Address(), ns)
			next.BindName(catalog.DescriptorNameKey(ns), ns.Address())
			e.Catalog.PublishLocked(next)
			e.Mu.Unlock()
			syntax := statisticsUnitCreate()
			syntax.Name = parser.ObjectName{Parts: []parser.Identifier{{Text: ns.Name()}, {Text: "s"}}}
			stage, err := statisticsUnitStage(t, e, op, syntax)
			if err != nil {
				t.Fatal(err)
			}
			defer e.DiscardCatalogStage(stage)
			_, holder := blockingUnitOwner(t, e)
			waiter, _ := blockingUnitOwner(t, e)
			scope := pgrowlocksUnitScope(t, e, waiter)
			resource := lock.Resource{ObjectRef: e.Catalog.Domain.Ref(stage.Base.Epoch(), ns.Address())}
			if err = e.Locks.Acquire(t.Context(), holder, resource, lock.AccessExclusive); err != nil {
				t.Fatal(err)
			}
			result := make(chan error, 1)
			go func() { result <- scope.AcquireCatalogNamespace(stage.Base, ns.Address(), syntax.Name) }()
			joined := false
			defer func() {
				e.EndLockOwner(holder)
				if !joined {
					<-result
				}
			}()
			enginetest.LockPending(t, e, 1)
			if e.CatalogRead().NameAddress(catalog.DescriptorNameKey(statisticsUnitDescriptor(t, stage))) != (ids.ObjectAddress{}) {
				t.Fatal("unadmitted stage visible")
			}
			e.Mu.Lock()
			retired := catalog.CloneState(e.Catalog.State)
			retired.RemoveDescriptor(ns.Address())
			retired.UnbindName(catalog.DescriptorNameKey(ns))
			if recreate {
				replacement := *ns
				replacement.ObjectAddress.ObjectID++
				replacement.ObjectRevision++
				retired.SetDescriptor(replacement.Address(), &replacement)
				retired.BindName(catalog.DescriptorNameKey(&replacement), replacement.Address())
			}
			e.Catalog.PublishLocked(retired)
			e.Mu.Unlock()
			incumbent := e.CatalogRead()
			e.EndLockOwner(holder)
			err = <-result
			joined = true
			if err == nil || e.CatalogRead().State() != incumbent.State() {
				t.Fatal("namespace waiter adopted successor", err)
			}
			prepared, validationErr := e.ValidateCatalogStage(t.Context(), stage)
			if validationErr == nil {
				_, validationErr = e.PublishCatalogOperation(op, nil, prepared, core.PublicationToken{Epoch: stage.Base.Epoch()})
			}
			if validationErr == nil || e.CatalogRead().State() != incumbent.State() {
				t.Fatal("retired namespace stage published", validationErr)
			}
		})
	}
}

func TestStatisticsObjectPublicationNameLock(t *testing.T) {
	e, _ := statisticsUnitEngine(t)
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	stage, err := statisticsUnitStage(t, e, op, statisticsUnitCreate())
	if err != nil {
		t.Fatal(err)
	}
	defer e.DiscardCatalogStage(stage)
	definition := statisticsUnitDescriptor(t, stage)
	key := catalog.DescriptorNameKey(definition)
	namespace := e.Catalog.Domain.Ref(stage.Base.Epoch(), key.Namespace)
	resource := lock.Resource{ObjectRef: namespace, Name: key}
	_, holder := blockingUnitOwner(t, e)
	waiter, _ := blockingUnitOwner(t, e)
	scope := pgrowlocksUnitScope(t, e, waiter)
	if err = e.Locks.Acquire(t.Context(), holder, resource, lock.AccessExclusive); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { result <- scope.AcquireCatalogResource(resource, lock.AccessExclusive) }()
	joined := false
	defer func() {
		e.EndLockOwner(holder)
		if !joined {
			<-result
		}
	}()
	enginetest.LockPending(t, e, 1)
	// A distinct exact name does not serialize with this pending destination.
	distinct := resource
	distinct.Name.Name = "statistics_independent"
	if err = e.Locks.Acquire(t.Context(), holder, distinct, lock.AccessExclusive); err != nil {
		t.Fatal(err)
	}
	winner, err := statisticsUnitStage(t, e, op, statisticsUnitCreate())
	if err != nil {
		t.Fatal(err)
	}
	defer e.DiscardCatalogStage(winner)
	winning := statisticsUnitDescriptor(t, winner)
	extensionUnitPublish(t, e, op, winner)
	incumbent := e.CatalogRead()
	e.EndLockOwner(holder)
	err = <-result
	joined = true
	if err != nil {
		t.Fatal(err)
	}
	if !e.Locks.Holds(scope.LockTicket, resource, lock.AccessExclusive) {
		t.Fatal("waiter did not retain exact-name grant")
	}
	claim := catalog.BindingClaim{Key: key, Address: definition.Address(), Priority: 1}
	if err = catalog.BindingCurrent(stage.Base, incumbent, claim); err == nil {
		t.Fatal("name waiter adopted concurrent definition")
	}
	if e.CatalogRead().State() != incumbent.State() || incumbent.Get(winning.Address()) != winning || winning.Address() == definition.Address() {
		t.Fatal("losing stage changed winner")
	}
	// Failed admission must preserve the original relation and immutable selection.
	if winning.Relation != definition.Relation || winning.Selection != definition.Selection {
		t.Fatal("concurrent creation changed definition facts")
	}
}
