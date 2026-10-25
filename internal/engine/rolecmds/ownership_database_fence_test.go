package rolecmds_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
)

func TestRoleOwnershipDatabaseStagePredecessor(t *testing.T) {
	for _, private := range []bool{false, true} {
		t.Run(map[bool]string{false: "committed", true: "private"}[private], func(t *testing.T) {
			e := engine.New()
			op, done := enginetest.RoleOperation(t, e, "postgres")
			defer done()
			e.Mu.Lock()
			defer e.Mu.Unlock()
			auth, err := e.AuthorizationLocked(op)
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
				auth.Catalog = e.Catalog.Read(selected)
				auth.Recapture(auth.Catalog)
			}
			stage := e.BeginCatalogStage(auth.Catalog)
			defer e.DiscardCatalogStage(stage)
			stage.AuthorizeLocked(auth, op.Session)
			changes := session.NewJournal()
			authority := auth.CatalogAuthorityLocked(ids.ObjectRef{}, nil)
			// Empty descriptor/removal sets are deliberate: storage-only TRUNCATE and
			// other staged authorities must still record the database predecessor.
			if err := core.EngineCaptureCatalogStageLocked(e, stage, changes, auth.Catalog.State(), authority); err != nil {
				t.Fatal(err)
			}
			if changes.Originals[catalog.CurrentDatabaseAddress] != committed || authority.DatabasePredecessor != committed || authority.DatabaseAuthority != auth.Catalog.Get(catalog.CurrentDatabaseAddress) {
				t.Fatal("entry/private predecessor pairing")
			}
			child := changes.Clone()
			released := child.Clone()
			if child.Originals[catalog.CurrentDatabaseAddress] != committed || released.Originals[catalog.CurrentDatabaseAddress] != committed {
				t.Fatal("savepoint clone lost predecessor")
			}
			if err := e.CatalogPreflightLocked(released); err != nil {
				t.Fatal("unchanged predecessor rejected", err)
			}
			next := catalog.CloneState(e.Catalog.State)
			database := *committed.(*catalog.Database)
			database.ObjectRevision++
			next.SetDescriptor(catalog.CurrentDatabaseAddress, &database)
			e.Catalog.PublishLocked(next)
			if err := e.CatalogPreflightLocked(released); err == nil {
				t.Fatal("outer commit accepted changed database predecessor")
			}
			if err := core.EngineCaptureCatalogStageLocked(e, stage, session.NewJournal(), auth.Catalog.State(), nil); !errors.Is(err, ids.ErrCatalogReplaced) {
				t.Fatal("Finish accepted changed committed predecessor", err)
			}
			if auth.Catalog.Get(catalog.CurrentDatabaseAddress) != stage.DatabaseAuthority || auth.RoleRecord.Revision != e.RoleStore.Published.Revision {
				t.Fatal("retained authority mutated or artificial role revision")
			}
			implicit := auth.Roles.LookupRef("pg_database_owner")
			for _, edge := range fixtureEdges(auth.RoleRecord) {
				if edge.Role == implicit {
					t.Fatal("database-owner authority invented explicit membership", edge)
				}
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
			sess, err := f.e.StartSession(context.Background(), session.SessionStart{ID: ids.SessionID(testSessionID.Add(1)), Authenticated: f.owner})
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				sess.RequestEnd(session.CleanClose)
				if err := sess.WaitEnd(context.Background()); err != nil {
					t.Error(err)
				}
			}()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			message := batchUnitStart(t, sess, ctx)
			defer message.end(context.Canceled)
			oldCatalog, oldRoles := f.e.Catalog.State, f.e.RoleStore.Published
			relations := make(map[ids.ObjectAddress]*storage.Relation, len(f.e.Storage.Relations))
			for address, root := range f.e.Storage.Relations {
				relations[address] = root
			}
			sessionRevision := sess.Authorization.Revision
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
			// Scope-owned candidates enter the owner journal only at Finish; the
			// message still owns final publication and its database/context fences.
			if err := message.scope.Finish(nil); err != nil {
				t.Fatal(err)
			}
			retained, err := core.OperationAuthorization(message.op)
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
			if f.e.Catalog.State != oldCatalog || f.e.RoleStore.Published != oldRoles || oldCatalog.DefaultACLCount() != 0 {
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
			current := f.e.Catalog.State
			if f.e.RoleStore.Published != oldRoles || current.Epoch() != oldCatalog.Epoch() || sess.Authorization.Revision != sessionRevision {
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
			if f.e.Catalog.State != current || f.e.RoleStore.Published != oldRoles || current.DefaultACLCount() != 0 || len(f.e.Storage.Relations) != len(relations) {
				t.Fatal("veto published catalog, role or default-policy prefix")
			}
			for address, root := range relations {
				if f.e.Storage.Relations[address] != root {
					t.Fatal("veto published a storage prefix")
				}
			}
			if retained.Catalog.State().DefaultACL(key) != policy || retained.Catalog.Get(catalog.CurrentDatabaseAddress) != &database || retained.RoleRecord != pendingRoles || oldCatalog.Get(catalog.CurrentDatabaseAddress) != &database {
				t.Fatal("final veto mutated retained authority roots")
			}
		})
	}
}
