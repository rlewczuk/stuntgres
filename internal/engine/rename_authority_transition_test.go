package engine

import (
	"context"
	"database/sql/driver"
	"reflect"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"sync"
	"testing"
	"time"
)

// Ordinary table owner in a separately owned namespace: USAGE and CREATE are
// independent host grants, so CREATE can be revoked without hiding the source.
func renameAuthorityFixture(t *testing.T) (*Engine, *Session, *Session, ids.ObjectRef, authorization.ACLValue) {
	t.Helper()
	e := New()
	admin, owner := sessionStart(t, e, nil), sessionStart(t, e, nil)
	sessionSQL(t, admin, "CREATE USER rename_transition_owner")
	sessionSQL(t, admin, "CREATE SCHEMA protected")
	actor, _ := e.Roles().Lookup("rename_transition_owner")
	view := e.CatalogRead()
	address := view.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "protected"})
	ns := e.catalog.Domain.Ref(view.Epoch(), address)
	acl := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: view.MustNamespace(address).Owner, Grantee: authorization.ACLGrantee{Role: actor.Ref}, Privileges: authorization.UsagePrivilege | authorization.CreatePrivilege}}}
	if err := e.SetObjectACL(context.Background(), ns, acl); err != nil {
		t.Fatal(err)
	}
	sessionSQL(t, owner, "SET SESSION AUTHORIZATION rename_transition_owner")
	sessionSQL(t, owner, "CREATE TABLE protected.t(a INT CONSTRAINT nn NOT NULL,b TEXT)")
	sessionSQL(t, owner, "INSERT INTO protected.t VALUES(7,'keep')")
	return e, admin, owner, ns, acl
}

func TestAlterRenameAuthorityWaitTransition(t *testing.T) {
	e, admin, owner, ns, acl := renameAuthorityFixture(t)
	ref := roleTableRef(t, e, "protected.t")
	old := e.catalog.state.Get(ref.Address)
	storage := e.storage.relations[ref.Address]
	tx := transactionBegin(t, admin)
	sessionSQL(t, admin, "LOCK TABLE protected.t IN ACCESS EXCLUSIVE MODE")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := lockAsync(e, owner, ctx, "ALTER TABLE protected.t RENAME TO renamed", false)
	lockPending(t, e, 1)
	acl.Entries[0].Privileges = authorization.UsagePrivilege
	if err := e.SetObjectACL(context.Background(), ns, acl); err != nil {
		t.Fatal(err)
	}
	frontier := e.catalog.nextRevision
	transactionEnd(t, admin, tx, false)
	// The confirmed waiter must recheck CREATE after its AX grant.
	select {
	case err := <-done:
		renameWantError(t, err, "42501", "permission denied for schema protected")
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	lockEmpty(t, e)
	if roleTableRef(t, e, "protected.t") != ref || e.catalog.state.Get(ref.Address) != old || e.storage.relations[ref.Address] != storage || e.catalog.nextRevision != frontier {
		t.Fatal("grant-time denial changed source or frontier")
	}
	if _, exists := e.catalog.state.LookupName(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ns.Address, Name: "renamed"}); exists {
		t.Fatal("grant-time denial published destination")
	}
	result := sessionSQL(t, owner, "SELECT * FROM protected.t")
	if !reflect.DeepEqual(tuples(result), [][]driver.Value{{int64(7), "keep"}}) {
		t.Fatal("denial changed rows")
	}
	sessionSQL(t, owner, "ALTER TABLE protected.t RENAME a TO renamed_column")
	lockEmpty(t, e)
}

func TestAlterRenameAuthorityValidationTransition(t *testing.T) {
	e, _, owner, ns, acl := renameAuthorityFixture(t)
	ref := roleTableRef(t, e, "protected.t")
	old := e.catalog.state.Get(ref.Address)
	storage := e.storage.relations[ref.Address]
	oldNamespace := e.catalog.state.Get(ns.Address)
	acl.Entries[0].Privileges = authorization.UsagePrivilege
	var once sync.Once
	provider := e.catalog.Providers[ids.RelationClassID]
	var frontier ids.DefinitionRevision
	e.catalog.Providers[ids.RelationClassID] = alterDropProvider{Provider: provider, callback: func() {
		once.Do(func() {
			if err := e.SetObjectACL(context.Background(), ns, acl); err != nil {
				t.Fatal(err)
			}
			frontier = e.catalog.nextRevision
		})
	}}
	op, err := owner.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.Execute(op, statement(t, "ALTER TABLE protected.t RENAME TO renamed"), 0, nil)
	op.Done()
	if err != (catalog.Conflict{}) {
		t.Fatalf("namespace revision fence got %T %v, want CatalogConflict", err, err)
	}
	if e.catalog.state.Get(ns.Address) == oldNamespace || e.catalog.state.Get(ref.Address) != old || e.storage.relations[ref.Address] != storage || e.catalog.nextRevision != frontier || roleTableRef(t, e, "protected.t") != ref {
		t.Fatal("namespace change not retained or failed rename published effects")
	}
	if _, exists := e.catalog.state.LookupName(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ns.Address, Name: "renamed"}); exists {
		t.Fatal("validation denial published destination")
	}
	lockEmpty(t, e)
}

func TestAlterRenameIfExistsUnauthorizedNamespace(t *testing.T) {
	e, _, owner, ns, _ := renameAuthorityFixture(t)
	if err := e.SetObjectACL(context.Background(), ns, authorization.ACLValue{Explicit: true}); err != nil {
		t.Fatal(err)
	}
	ref := roleTableRef(t, e, "protected.t")
	old := e.catalog.state.Get(ref.Address)
	storage := e.storage.relations[ref.Address]
	frontier := e.catalog.nextRevision
	op, err := owner.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	for _, q := range []string{"ALTER TABLE IF EXISTS protected.missing RENAME a TO b", "ALTER TABLE IF EXISTS protected.missing RENAME TO renamed"} {
		stmt := statement(t, q)
		if _, err = e.Describe(op, stmt); err != nil {
			t.Fatal("conditional utility Prepare checked namespace", err)
		}
		_, err = e.Execute(op, stmt, 0, nil)
		renameWantError(t, err, "42501", "permission denied for schema protected")
		if notices := op.DetachNotices(); len(notices) != 0 {
			t.Fatal("unauthorized namespace emitted skip notice", notices)
		}
	}
	if e.catalog.state.Get(ref.Address) != old || e.storage.relations[ref.Address] != storage || e.catalog.nextRevision != frontier {
		t.Fatal("unauthorized conditional mutated source")
	}
	lockEmpty(t, e)
}
