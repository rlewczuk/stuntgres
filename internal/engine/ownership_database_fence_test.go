package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
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
			committed := auth.Catalog.Get(catalog.CurrentDatabaseAddress)
			if private {
				selected := catalog.CloneState(auth.Catalog.State())
				database := *committed.(*catalog.Database)
				database.ACL = authorization.ACLValue{Explicit: true}
				database.ObjectRevision++
				selected.SetDescriptor(catalog.CurrentDatabaseAddress, &database)
				auth.Catalog = e.catalog.read(selected)
				auth.Recapture(auth.Catalog)
			}
			stage := e.BeginCatalogStage(auth.Catalog)
			defer e.DiscardCatalogStage(stage)
			stage.authorizeLocked(auth, op.Session)
			changes := session.NewJournal()
			authority := auth.catalogAuthorityLocked(ids.ObjectRef{}, nil)
			// Empty descriptor/removal sets are deliberate: storage-only TRUNCATE and
			// other staged authorities must still record the database predecessor.
			if err := e.captureCatalogStageLocked(stage, changes, auth.Catalog.State(), authority); err != nil {
				t.Fatal(err)
			}
			if changes.Originals[catalog.CurrentDatabaseAddress] != committed || authority.databasePredecessor != committed || authority.databaseAuthority != auth.Catalog.Get(catalog.CurrentDatabaseAddress) {
				t.Fatal("entry/private predecessor pairing")
			}
			child := changes.Clone()
			released := child.Clone()
			if child.Originals[catalog.CurrentDatabaseAddress] != committed || released.Originals[catalog.CurrentDatabaseAddress] != committed {
				t.Fatal("savepoint clone lost predecessor")
			}
			if err := e.catalogPreflightLocked(released); err != nil {
				t.Fatal("unchanged predecessor rejected", err)
			}
			next := catalog.CloneState(e.catalog.state)
			database := *committed.(*catalog.Database)
			database.ObjectRevision++
			next.SetDescriptor(catalog.CurrentDatabaseAddress, &database)
			e.catalog.state = next
			if err := e.catalogPreflightLocked(released); err == nil {
				t.Fatal("outer commit accepted changed database predecessor")
			}
			if err := e.captureCatalogStageLocked(stage, session.NewJournal(), auth.Catalog.State(), nil); !errors.Is(err, ids.ErrCatalogReplaced) {
				t.Fatal("Finish accepted changed committed predecessor", err)
			}
			if auth.Catalog.Get(catalog.CurrentDatabaseAddress) != stage.databaseAuthority || auth.RoleRecord.Revision != e.roles.published.Revision {
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
			database := *setup.Base.MustDatabase(catalog.CurrentDatabaseAddress)
			database.Owner = f.owner
			database.ObjectRevision++
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
			oldCatalog, oldRoles := f.e.catalog.state, f.e.roles.published
			relations := make(map[ids.ObjectAddress]*storage.Relation, len(f.e.storage.Relations))
			for address, root := range f.e.storage.Relations {
				relations[address] = root
			}
			sessionRevision := session.Authorization.revision
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
			retained, err := operationAuthorization(message.op)
			if err != nil {
				t.Fatal(err)
			}
			dbRole := retained.Roles.LookupRef("pg_database_owner")
			key := catalog.DefaultACLKey{Creator: dbRole, Class: authorization.DefaultTypes}
			policy := retained.Catalog.State().DefaultACL(key)
			pendingRoles := retained.RoleRecord
			if policy == nil || pendingRoles == oldRoles || retained.Catalog.State() == oldCatalog || retained.PairedCatalog.State() != retained.Catalog.State() || retained.Catalog.Get(catalog.CurrentDatabaseAddress) != &database {
				t.Fatal("missing paired private default/role/DB authority")
			}
			if f.e.catalog.state != oldCatalog || f.e.roles.published != oldRoles || oldCatalog.DefaultACLCount() != 0 {
				t.Fatal("utility published a prefix before message completion")
			}
			if veto == "database" {
				replacement := database
				replacement.Owner = f.reader
				replacement.ObjectRevision++
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
			if f.e.roles.published != oldRoles || current.Epoch() != oldCatalog.Epoch() || session.Authorization.revision != sessionRevision {
				t.Fatal("intervening DB change altered unrelated authority")
			}
			finishErr := message.scope.Finish(nil)
			err = message.end(finishErr)
			if veto == "cancel" {
				if !errors.Is(err, context.Canceled) {
					t.Fatal("missing final context veto", err)
				}
			} else {
				var conflict catalog.Conflict
				if !errors.Is(err, ids.ErrCatalogReplaced) && !errors.As(err, &conflict) {
					t.Fatal("missing final DB predecessor veto", err)
				}
			}
			if f.e.catalog.state != current || f.e.roles.published != oldRoles || current.DefaultACLCount() != 0 || len(f.e.storage.Relations) != len(relations) {
				t.Fatal("veto published catalog, role or default-policy prefix")
			}
			for address, root := range relations {
				if f.e.storage.Relations[address] != root {
					t.Fatal("veto published a storage prefix")
				}
			}
			if retained.Catalog.State().DefaultACL(key) != policy || retained.Catalog.Get(catalog.CurrentDatabaseAddress) != &database || retained.RoleRecord != pendingRoles || oldCatalog.Get(catalog.CurrentDatabaseAddress) != &database {
				t.Fatal("final veto mutated retained authority roots")
			}
		})
	}
}
