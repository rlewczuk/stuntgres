package tablecmds_test

import (
	"context"
	"database/sql/driver"
	"reflect"
	"sync"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
)

// Ordinary table owner in a separately owned namespace: USAGE and CREATE are
// independent host grants, so CREATE can be revoked without hiding the source.
func renameAuthorityFixture(t *testing.T) (*engine.Engine, *session.Session, *session.Session, ids.ObjectRef, authorization.ACLValue) {
	t.Helper()
	e := engine.New()
	admin, owner := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, admin, "CREATE USER rename_transition_owner")
	enginetest.SessionSQL(t, admin, "CREATE SCHEMA protected")
	actor, _ := e.Roles().Lookup("rename_transition_owner")
	view := e.CatalogRead()
	address := view.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "protected"})
	ns := e.Catalog.Domain.Ref(view.Epoch(), address)
	acl := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: view.MustNamespace(address).Owner, Grantee: authorization.ACLGrantee{Role: actor.Ref}, Privileges: authorization.UsagePrivilege | authorization.CreatePrivilege}}}
	if err := enginetest.SetObjectACL(e, context.Background(), ns, acl); err != nil {
		t.Fatal(err)
	}
	enginetest.SessionSQL(t, owner, "SET SESSION AUTHORIZATION rename_transition_owner")
	enginetest.SessionSQL(t, owner, "CREATE TABLE protected.t(a INT CONSTRAINT nn NOT NULL,b TEXT)")
	enginetest.SessionSQL(t, owner, "INSERT INTO protected.t VALUES(7,'keep')")
	return e, admin, owner, ns, acl
}

func TestAlterRenameAuthorityWaitTransition(t *testing.T) {
	e, admin, owner, ns, acl := renameAuthorityFixture(t)
	ref := enginetest.RoleTableRef(t, e, "protected.t")
	old := e.Catalog.State.Get(ref.Address)
	storage := e.Storage.Relations[ref.Address]
	tx := enginetest.TransactionBegin(t, admin)
	enginetest.SessionSQL(t, admin, "LOCK TABLE protected.t IN ACCESS EXCLUSIVE MODE")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := enginetest.LockAsync(e, owner, ctx, "ALTER TABLE protected.t RENAME TO renamed", false)
	enginetest.LockPending(t, e, 1)
	acl.Entries[0].Privileges = authorization.UsagePrivilege
	if err := enginetest.SetObjectACL(e, context.Background(), ns, acl); err != nil {
		t.Fatal(err)
	}
	frontier := e.Catalog.NextRevision()
	enginetest.TransactionEnd(t, admin, tx, false)
	// The confirmed waiter must recheck CREATE after its AX grant.
	select {
	case err := <-done:
		renameWantError(t, err, "42501", "permission denied for schema protected")
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	enginetest.LockEmpty(t, e)
	if enginetest.RoleTableRef(t, e, "protected.t") != ref || e.Catalog.State.Get(ref.Address) != old || e.Storage.Relations[ref.Address] != storage || e.Catalog.NextRevision() != frontier {
		t.Fatal("grant-time denial changed source or frontier")
	}
	if _, exists := e.Catalog.State.LookupName(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ns.Address, Name: "renamed"}); exists {
		t.Fatal("grant-time denial published destination")
	}
	result := enginetest.SessionSQL(t, owner, "SELECT * FROM protected.t")
	if !reflect.DeepEqual(enginetest.Tuples(result), [][]driver.Value{{int64(7), "keep"}}) {
		t.Fatal("denial changed rows")
	}
	enginetest.SessionSQL(t, owner, "ALTER TABLE protected.t RENAME a TO renamed_column")
	enginetest.LockEmpty(t, e)
}

func TestAlterRenameAuthorityValidationTransition(t *testing.T) {
	e, _, owner, ns, acl := renameAuthorityFixture(t)
	ref := enginetest.RoleTableRef(t, e, "protected.t")
	old := e.Catalog.State.Get(ref.Address)
	storage := e.Storage.Relations[ref.Address]
	oldNamespace := e.Catalog.State.Get(ns.Address)
	acl.Entries[0].Privileges = authorization.UsagePrivilege
	var once sync.Once
	provider := e.Catalog.Providers[ids.RelationClassID]
	var frontier ids.DefinitionRevision
	e.Catalog.Providers[ids.RelationClassID] = alterDropProvider{Provider: provider, callback: func() {
		once.Do(func() {
			if err := enginetest.SetObjectACL(e, context.Background(), ns, acl); err != nil {
				t.Fatal(err)
			}
			frontier = e.Catalog.NextRevision()
		})
	}}
	op, err := owner.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.Execute(op, enginetest.Statement(t, "ALTER TABLE protected.t RENAME TO renamed"), 0, nil)
	op.Done()
	if err != (catalog.Conflict{}) {
		t.Fatalf("namespace revision fence got %T %v, want CatalogConflict", err, err)
	}
	if e.Catalog.State.Get(ns.Address) == oldNamespace || e.Catalog.State.Get(ref.Address) != old || e.Storage.Relations[ref.Address] != storage || e.Catalog.NextRevision() != frontier || enginetest.RoleTableRef(t, e, "protected.t") != ref {
		t.Fatal("namespace change not retained or failed rename published effects")
	}
	if _, exists := e.Catalog.State.LookupName(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ns.Address, Name: "renamed"}); exists {
		t.Fatal("validation denial published destination")
	}
	enginetest.LockEmpty(t, e)
}

func TestAlterRenameIfExistsUnauthorizedNamespace(t *testing.T) {
	e, _, owner, ns, _ := renameAuthorityFixture(t)
	if err := enginetest.SetObjectACL(e, context.Background(), ns, authorization.ACLValue{Explicit: true}); err != nil {
		t.Fatal(err)
	}
	ref := enginetest.RoleTableRef(t, e, "protected.t")
	old := e.Catalog.State.Get(ref.Address)
	storage := e.Storage.Relations[ref.Address]
	frontier := e.Catalog.NextRevision()
	op, err := owner.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	for _, q := range []string{"ALTER TABLE IF EXISTS protected.missing RENAME a TO b", "ALTER TABLE IF EXISTS protected.missing RENAME TO renamed"} {
		stmt := enginetest.Statement(t, q)
		if _, err = e.Describe(op, stmt); err != nil {
			t.Fatal("conditional utility Prepare checked namespace", err)
		}
		_, err = e.Execute(op, stmt, 0, nil)
		renameWantError(t, err, "42501", "permission denied for schema protected")
		if notices := op.DetachNotices(); len(notices) != 0 {
			t.Fatal("unauthorized namespace emitted skip notice", notices)
		}
	}
	if e.Catalog.State.Get(ref.Address) != old || e.Storage.Relations[ref.Address] != storage || e.Catalog.NextRevision() != frontier {
		t.Fatal("unauthorized conditional mutated source")
	}
	enginetest.LockEmpty(t, e)
}
