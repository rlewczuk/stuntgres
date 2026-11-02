package schemacmds_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/schemacmds"
	"stuntgres/internal/parser"
)

// Delegate genuine provider validation before exposing its outside-Mu gap.
type dependencyValidationGate struct {
	catalog.Provider
	engine           *core.Engine
	name             string
	entered, release chan struct{}
	once             atomic.Bool
}

func (g *dependencyValidationGate) Validate(read catalog.Read, d catalog.Descriptor) error {
	if err := g.Provider.Validate(read, d); err != nil {
		return err
	}
	if ext, ok := d.(*catalog.Extension); ok && ext.Name() == g.name && g.once.CompareAndSwap(false, true) {
		if !g.engine.Mu.TryLock() {
			return errors.New("dependency validation held Engine.Mu")
		}
		g.engine.Mu.Unlock()
		close(g.entered)
		select {
		case <-g.release:
		case <-time.After(5 * time.Second):
			return errors.New("dependency validation gate deadline")
		}
	}
	return nil
}

func TestExtensionDependencyPublication(t *testing.T) {
	for _, reason := range []string{"target_namespace", "prerequisite_namespace", "explicit_overridden_namespace", "prerequisite_descriptor", "prerequisite_member", "roles", "database", "defaults", "cancel", "unrelated_namespace"} {
		t.Run(reason, func(t *testing.T) {
			e := engine.New()
			schemas := dependencyUnitNamespaces(t, e, "fence_target", "fence_cube", "fence_explicit", "fence_unrelated")
			admin, adminDone := enginetest.RoleOperation(t, e, "postgres")
			defer adminDone()
			extensionUnitPublish(t, e, admin, cubeUnitStage(t, e, admin, "cube", "fence_cube"))
			cube := e.CatalogRead().MustExtension(e.CatalogRead().NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "cube"}))
			name, schema := "earthdistance", "fence_target"
			if reason == "explicit_overridden_namespace" {
				enginetest.RoleExecute(t, e, admin, "DROP EXTENSION plpgsql")
				name, schema = "plpgsql", "fence_explicit"
			}
			worker, workerDone := enginetest.RoleOperation(t, e, "postgres")
			defer workerDone()
			ctx, cancel := context.WithCancel(worker.Context())
			defer cancel()
			worker.Ctx = ctx
			scope, err := e.StartStatement(worker)
			if err != nil {
				t.Fatal(err)
			}
			creation := &parser.CreateExtension{Name: parser.Identifier{Text: name}, Options: []parser.ExtensionOption{{Name: "cascade"}, {Name: "schema", Value: parser.Identifier{Text: schema}}}}
			// Exercise the real Finish publication/transaction frontier, not an
			// unclassified scope whose pending journal is intentionally ignored.
			if err := scope.Gate(creation); err != nil {
				scope.Finish(err)
				t.Fatal(err)
			}
			e.Mu.Lock()
			stage, err := schemacmds.StageExtensionLocked(e, scope, creation)
			e.Mu.Unlock()
			defer e.DiscardCatalogStage(stage)
			if err != nil {
				scope.Finish(err)
				t.Fatal(err)
			}
			observed := make(map[ids.ObjectAddress]bool)
			for _, namespace := range schemacmds.ExtensionNamespacesOf(stage) {
				observed[namespace.Address] = true
			}
			if !observed[schemas[schema]] {
				scope.Finish(errors.New("missing explicit schema observation"))
				t.Fatal("explicit schema not observed")
			}
			if name == "earthdistance" && !observed[schemas["fence_cube"]] {
				scope.Finish(errors.New("missing prerequisite namespace"))
				t.Fatal("actual prerequisite namespace not observed")
			}
			if name == "plpgsql" && !observed[ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}] {
				scope.Finish(errors.New("missing fixed target"))
				t.Fatal("fixed script target not observed")
			}
			gate := &dependencyValidationGate{Provider: e.Catalog.Providers[ids.ExtensionClassID], engine: e, name: name, entered: make(chan struct{}), release: make(chan struct{})}
			e.Catalog.Providers[ids.ExtensionClassID] = gate
			var release sync.Once
			unblock := func() { release.Do(func() { close(gate.release) }) }
			finished := make(chan error, 1)
			go func() {
				_, failure := schemacmds.PublishExtensionStage(e, scope, stage)
				finished <- scope.Finish(failure)
			}()
			joined := false
			defer func() {
				cancel()
				unblock()
				if !joined {
					select {
					case <-finished:
					case <-time.After(5 * time.Second):
						t.Error("dependency publication cleanup deadline")
					}
				}
			}()
			select {
			case <-gate.entered:
			case failure := <-finished:
				joined = true
				t.Fatal("did not reach genuine validation gap", failure)
			case <-time.After(5 * time.Second):
				t.Fatal("dependency validation admission deadline")
			}
			if e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: name}) != (ids.ObjectAddress{}) {
				t.Fatal("closure published before validation")
			}
			if reason == "defaults" {
				enginetest.RoleExecute(t, e, admin, "ALTER DEFAULT PRIVILEGES REVOKE USAGE ON TYPES FROM PUBLIC")
			} else {
				e.Mu.Lock()
				next := catalog.CloneState(e.Catalog.State)
				switch reason {
				case "target_namespace", "prerequisite_namespace", "explicit_overridden_namespace", "unrelated_namespace":
					key := map[string]string{"target_namespace": "fence_target", "prerequisite_namespace": "fence_cube", "explicit_overridden_namespace": "fence_explicit", "unrelated_namespace": "fence_unrelated"}[reason]
					copy := *next.MustNamespace(schemas[key])
					copy.ObjectRevision++
					next.SetDescriptor(copy.Address(), &copy)
				case "prerequisite_descriptor":
					copy := *cube
					copy.ObjectRevision++
					next.SetDescriptor(copy.Address(), &copy)
				case "prerequisite_member":
					copy := *next.MustType(cube.Members[0])
					copy.ObjectRevision++
					next.SetDescriptor(copy.Address(), &copy)
				case "roles":
					e.RoleStore.PublishLocked(e.RoleStore.Published.Advance())
				case "database":
					copy := *next.MustDatabase(catalog.CurrentDatabaseAddress)
					copy.ObjectRevision++
					next.SetDescriptor(copy.Address(), &copy)
				case "cancel":
					cancel()
				}
				e.Catalog.PublishLocked(next)
				e.Mu.Unlock()
			}
			winner := e.CatalogRead()
			unblock()
			select {
			case err = <-finished:
				joined = true
			case <-time.After(5 * time.Second):
				t.Fatal("dependency publication join deadline")
			}
			if reason == "unrelated_namespace" {
				if err != nil {
					t.Fatal("unrelated schema caused false conflict", err)
				}
				published := e.CatalogRead()
				address := published.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: name})
				installed, exists := published.Extension(address)
				if !exists || installed.Name() != name || installed.Installation == nil || !installed.Installation.Valid() {
					t.Fatal("successful closure lost installed extension", address)
				}
				if err := extension.StockEarthdistance.ValidateExtension(published, installed); err != nil {
					t.Fatal("successful closure lost its genuine dependency graph", err)
				}
				unrelated := published.MustNamespace(schemas["fence_unrelated"])
				expected := winner.MustNamespace(schemas["fence_unrelated"])
				if unrelated.Owner != expected.Owner || !reflect.DeepEqual(unrelated, expected) {
					t.Fatal("successful closure changed unrelated winner identity/revision/authority", unrelated, expected)
				}
				if err := (catalog.NamespaceProvider{}).Validate(published, unrelated); err != nil {
					t.Fatal("unrelated winner lost namespace authority", err)
				}
			} else {
				if err == nil {
					t.Fatal("stale/canceled closure published", reason)
				}
				if reason == "cancel" && !errors.Is(err, context.Canceled) {
					t.Fatal("cancellation boundary", err)
				}
				if e.CatalogRead().State() != winner.State() || e.CatalogRead().NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: name}) != (ids.ObjectAddress{}) {
					t.Fatal("failed closure changed winner state")
				}
			}
			if stage.Create(&catalog.Extension{ObjectAddress: ids.ObjectAddress{ClassID: ids.ExtensionClassID, ObjectID: 999999}, ObjectName: "after_discard"}) == nil {
				t.Fatal("completed/discarded stage accepted a write")
			}
		})
	}
	t.Run("committed_absence_requires_an_explicit_namespace_address", func(t *testing.T) {
		e := engine.New()
		missing := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 900001}
		observation := extension.AuthorityObservation{Roles: e.RoleStore.Published.Revision, NamespaceAddress: missing, Database: e.Catalog.State.Get(catalog.CurrentDatabaseAddress)}
		if err := observation.Current(nil, e.Catalog.State, e.RoleStore.Published.Revision); err != nil {
			t.Fatal("genuine committed absence rejected", err)
		}
		copy := observation
		copy.NamespaceAddress = ids.ObjectAddress{}
		if copy.Current(nil, e.Catalog.State, e.RoleStore.Published.Revision) == nil {
			t.Fatal("unset namespace mistaken for absence")
		}
		copy.NamespaceAddress = ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: missing.ObjectID}
		if copy.Current(nil, e.Catalog.State, e.RoleStore.Published.Revision) == nil {
			t.Fatal("wrong-class namespace mistaken for absence")
		}
		next := catalog.CloneState(e.Catalog.State)
		next.SetDescriptor(missing, &catalog.Namespace{ObjectAddress: missing, ObjectName: "appeared", ObjectRevision: 1, Owner: e.RoleStore.Domain.Principal(10)})
		if observation.Current(nil, next, e.RoleStore.Published.Revision) == nil {
			t.Fatal("committed namespace appearance bypassed absence fence")
		}
	})
	t.Run("transaction_private_schema_keeps_absence_predecessor", func(t *testing.T) {
		e := engine.New()
		op, done := enginetest.RoleOperation(t, e, "postgres")
		defer done()
		enginetest.RoleExecute(t, e, op, "BEGIN")
		defer enginetest.RoleExecute(t, e, op, "ROLLBACK")
		enginetest.RoleExecute(t, e, op, "CREATE SCHEMA dependency_private")
		scope, err := e.StartStatement(op)
		if err != nil {
			t.Fatal(err)
		}
		creation := &parser.CreateExtension{Name: parser.Identifier{Text: "cube"}, Options: []parser.ExtensionOption{{Name: "schema", Value: parser.Identifier{Text: "dependency_private"}}}}
		if err := scope.Gate(creation); err != nil {
			scope.Finish(err)
			t.Fatal(err)
		}
		e.Mu.Lock()
		stage, err := schemacmds.StageExtensionLocked(e, scope, creation)
		e.Mu.Unlock()
		defer e.DiscardCatalogStage(stage)
		if err != nil {
			scope.Finish(err)
			t.Fatal(err)
		}
		namespaces := schemacmds.ExtensionNamespacesOf(stage)
		if len(namespaces) != 1 || namespaces[0].Address.ObjectID == 0 || namespaces[0].Predecessor != nil || stage.Base.Get(namespaces[0].Address) == nil {
			scope.Finish(errors.New("wrong namespace predecessor"))
			t.Fatal("private admission was confused with committed predecessor")
		}
		// Capture the admitted installation before publication/disposal; later
		// assertions compare its logical identity, not descriptor clone pointers.
		cubeAddress := stage.LookupName(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "cube"})
		incumbent := *e.StageRead(stage).MustExtension(cubeAddress)
		_, err = schemacmds.PublishExtensionStage(e, scope, stage)
		if err = scope.Finish(err); err != nil {
			t.Fatal(err)
		}
		if e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "cube"}) != (ids.ObjectAddress{}) {
			t.Fatal("private transaction published cube")
		}
		dependentScope, err := e.StartStatement(op)
		if err != nil {
			t.Fatal(err)
		}
		creation = &parser.CreateExtension{Name: parser.Identifier{Text: "earthdistance"}, Options: []parser.ExtensionOption{{Name: "cascade"}, {Name: "schema", Value: parser.Identifier{Text: "dependency_private"}}}}
		if err := dependentScope.Gate(creation); err != nil {
			dependentScope.Finish(err)
			t.Fatal(err)
		}
		e.Mu.Lock()
		dependent, err := schemacmds.StageExtensionLocked(e, dependentScope, creation)
		e.Mu.Unlock()
		defer e.DiscardCatalogStage(dependent)
		if err != nil {
			dependentScope.Finish(err)
			t.Fatal("transaction-private incumbent lookup", err)
		}
		base, exists := dependent.Base.Extension(cubeAddress)
		if dependent.Get(cubeAddress) != nil || !exists {
			dependentScope.Finish(errors.New("private prerequisite not read from base"))
			t.Fatal("private prerequisite was replaced instead of reused", cubeAddress)
		}
		if base.Address() != incumbent.Address() || base.Revision() != incumbent.Revision() || base.Name() != incumbent.Name() || base.Version != incumbent.Version || base.TargetNamespace != incumbent.TargetNamespace || base.Owner != incumbent.Owner || base.Comment != incumbent.Comment || !slices.Equal(base.Members, incumbent.Members) {
			dependentScope.Finish(errors.New("private prerequisite identity changed"))
			t.Fatal("private prerequisite identity/revision/ownership changed")
		}
		if err := extension.StockCube.ValidateExtension(e.StageRead(dependent), base); err != nil {
			dependentScope.Finish(err)
			t.Fatal("reused private prerequisite lost genuine declaration authority", err)
		}
		_, err = schemacmds.PublishExtensionStage(e, dependentScope, dependent)
		if err == nil {
			original, observed := dependentScope.Catalog.Originals[cubeAddress]
			if !observed || original != nil {
				err = errors.New("private prerequisite lost its committed absence original")
			}
		}
		if err = dependentScope.Finish(err); err != nil {
			t.Fatal("private prerequisite observation transfer", err)
		}
		for _, name := range []string{"cube", "earthdistance"} {
			if e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: name}) != (ids.ObjectAddress{}) {
				t.Fatal("private closure leaked", name)
			}
		}
	})
	for _, cancelStage := range []bool{false, true} {
		name := "staging_error_discard"
		if cancelStage {
			name = "staging_cancel_discard"
		}
		t.Run(name, func(t *testing.T) {
			e := engine.New()
			providers := dependencyUnitRegister(e, map[string][]string{"a": {"b"}, "b": nil})
			op, done := enginetest.RoleOperation(t, e, "postgres")
			defer done()
			ctx, cancel := context.WithCancel(op.Context())
			defer cancel()
			op.Ctx = ctx
			var retained extension.Stage
			sentinel := errors.New("staged provider failure")
			providers["b"].after = func(stage extension.Stage, _ *catalog.Extension) error {
				retained = stage
				if cancelStage {
					cancel()
					return ctx.Err()
				}
				return sentinel
			}
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			creation := dependencyUnitCreation("a", "")
			if err := scope.Gate(creation); err != nil {
				scope.Finish(err)
				t.Fatal(err)
			}
			before := e.Catalog.State
			_, err = schemacmds.ExecuteExtension(e, scope, creation, 0)
			scope.Finish(err)
			want := sentinel
			if cancelStage {
				want = context.Canceled
			}
			if !errors.Is(err, want) || retained == nil || retained.Create(&catalog.Extension{ObjectAddress: ids.ObjectAddress{ClassID: ids.ExtensionClassID, ObjectID: 999998}, ObjectName: "discard_probe"}) == nil || e.Catalog.State != before {
				t.Fatal("failed candidate lifetime", err)
			}
			if !e.Mu.TryLock() {
				t.Fatal("staging cleanup retained engine mutex")
			}
			e.Mu.Unlock()
		})
	}
}
