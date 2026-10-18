package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"testing"
)

func TestRoleOwnershipDatabaseStagePredecessor(t *testing.T) {
	for _, private := range []bool{false, true} {
		t.Run(map[bool]string{false: "committed", true: "private"}[private], func(t *testing.T) {
			e := New()
			op, done := roleOperation(t, e, "postgres")
			defer done()
			e.mu.Lock()
			defer e.mu.Unlock()
			auth, err := e.authorizationLocked(op)
			if err != nil {
				t.Fatal(err)
			}
			committed := auth.catalog.Get(currentDatabaseAddress)
			if private {
				selected := cloneCatalog(auth.catalog.state)
				database := *committed.(*databaseDescriptor)
				database.acl = authorization.ACLValue{Explicit: true}
				database.revision++
				selected.descriptors[currentDatabaseAddress] = &database
				auth.catalog = e.catalog.read(selected)
				auth.recaptureCatalog(auth.catalog)
			}
			stage := e.BeginCatalogStage(auth.catalog)
			defer e.DiscardCatalogStage(stage)
			stage.authorizeLocked(auth, op.session)
			changes := newCatalogChanges()
			authority := auth.catalogAuthorityLocked(ids.ObjectRef{}, nil)
			// Empty descriptor/removal sets are deliberate: storage-only TRUNCATE and
			// other staged authorities must still record the database predecessor.
			if err := e.captureCatalogStageLocked(stage, changes, auth.catalog.state, authority); err != nil {
				t.Fatal(err)
			}
			if changes.originals[currentDatabaseAddress] != committed || authority.databasePredecessor != committed || authority.databaseAuthority != auth.catalog.Get(currentDatabaseAddress) {
				t.Fatal("entry/private predecessor pairing")
			}
			child := cloneCatalogChanges(changes)
			released := cloneCatalogChanges(child)
			if child.originals[currentDatabaseAddress] != committed || released.originals[currentDatabaseAddress] != committed {
				t.Fatal("savepoint clone lost predecessor")
			}
			if err := e.catalogPreflightLocked(released); err != nil {
				t.Fatal("unchanged predecessor rejected", err)
			}
			next := cloneCatalog(e.catalog.state)
			database := *committed.(*databaseDescriptor)
			database.revision++
			next.descriptors[currentDatabaseAddress] = &database
			e.catalog.state = next
			if err := e.catalogPreflightLocked(released); err == nil {
				t.Fatal("outer commit accepted changed database predecessor")
			}
			if err := e.captureCatalogStageLocked(stage, newCatalogChanges(), auth.catalog.state, nil); !errors.Is(err, ErrCatalogReplaced) {
				t.Fatal("Finish accepted changed committed predecessor", err)
			}
			if auth.catalog.Get(currentDatabaseAddress) != stage.databaseAuthority || auth.roleRecord.revision != e.roles.revision {
				t.Fatal("retained authority mutated or artificial role revision")
			}
		})
	}
}

// The default utility must fence the committed DB predecessor even when its
// only pending effects are default records and the selected private role root.
func TestRoleOwnershipDefaultMessageDatabaseFence(t *testing.T) {
	for _, veto := range []string{"database", "cancel"} {
		t.Run(veto, func(t *testing.T) {
			f := newDefaultUnitFixture(t)
			setup := f.e.BeginCatalogStage(f.e.CatalogRead())
			defaultUnitTable(t, f, setup, "default_fence_sentinel", f.owner)
			database := *setup.base.MustDatabase(currentDatabaseAddress)
			database.owner = f.owner
			database.revision++
			if err := setup.Replace(&database); err != nil {
				t.Fatal(err)
			}
			if err := defaultUnitPublish(t, f.e, setup); err != nil {
				t.Fatal(err)
			}
			f.e.DiscardCatalogStage(setup)
			session, err := f.e.StartSession(context.Background(), SessionStart{ID: ids.SessionID(testSessionID.Add(1)), Authenticated: f.owner})
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				session.RequestEnd(CleanClose)
				if err := session.WaitEnd(context.Background()); err != nil {
					t.Error(err)
				}
			}()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			message := batchUnitStart(t, session, ctx)
			defer message.end(context.Canceled)
			oldCatalog, oldRoles := f.e.catalog.state, f.e.roles
			storage := make(map[ids.ObjectAddress]*relationStorage, len(f.e.storage))
			for address, root := range f.e.storage {
				storage[address] = root
			}
			sessionRevision := session.authorization.revision
			prefix := defaultUnitStatement("tables", "select", []string{"default_owner"}, nil, []string{"default_reader"})
			if _, err := message.step(prefix); err != nil {
				t.Fatal(err)
			}
			message.scope, err = message.scope.NextStatement()
			if err != nil {
				t.Fatal(err)
			}
			selected := defaultUnitStatement("types", "usage", []string{"pg_database_owner"}, nil, []string{"default_reader"})
			if _, err := message.scope.Execute(selected, 0, nil); err != nil {
				t.Fatal("private DB owner authorization failed", err)
			}
			retained, err := message.op.Authorization()
			if err != nil {
				t.Fatal(err)
			}
			dbRole := retained.roles.LookupRef("pg_database_owner")
			key := defaultACLKey{creator: dbRole, class: authorization.DefaultTypes}
			policy := retained.catalog.state.defaultACLs[key]
			pendingRoles := retained.roleRecord
			if policy == nil || pendingRoles == oldRoles || retained.catalog.state == oldCatalog || retained.pairedCatalog.state != retained.catalog.state || retained.catalog.Get(currentDatabaseAddress) != &database {
				t.Fatal("missing paired private default/role/DB authority")
			}
			if f.e.catalog.state != oldCatalog || f.e.roles != oldRoles || len(oldCatalog.defaultACLs) != 0 {
				t.Fatal("utility published a prefix before message completion")
			}
			if veto == "database" {
				replacement := database
				replacement.owner = f.reader
				replacement.revision++
				other := f.e.BeginCatalogStage(f.e.CatalogRead())
				if err := other.Replace(&replacement); err != nil {
					t.Fatal(err)
				}
				if err := defaultUnitPublish(t, f.e, other); err != nil {
					t.Fatal(err)
				}
				f.e.DiscardCatalogStage(other)
			} else {
				cancel()
			}
			current := f.e.catalog.state
			if f.e.roles != oldRoles || current.epoch != oldCatalog.epoch || session.authorization.revision != sessionRevision {
				t.Fatal("intervening DB change altered unrelated authority")
			}
			finishErr := message.scope.Finish(nil)
			err = message.end(finishErr)
			if veto == "cancel" {
				if !errors.Is(err, context.Canceled) {
					t.Fatal("missing final context veto", err)
				}
			} else {
				var conflict CatalogConflict
				if !errors.Is(err, ErrCatalogReplaced) && !errors.As(err, &conflict) {
					t.Fatal("missing final DB predecessor veto", err)
				}
			}
			if f.e.catalog.state != current || f.e.roles != oldRoles || len(current.defaultACLs) != 0 || len(f.e.storage) != len(storage) {
				t.Fatal("veto published catalog, role or default-policy prefix")
			}
			for address, root := range storage {
				if f.e.storage[address] != root {
					t.Fatal("veto published a storage prefix")
				}
			}
			if retained.catalog.state.defaultACLs[key] != policy || retained.catalog.Get(currentDatabaseAddress) != &database || retained.roleRecord != pendingRoles || oldCatalog.Get(currentDatabaseAddress) != &database {
				t.Fatal("final veto mutated retained authority roots")
			}
		})
	}
}
