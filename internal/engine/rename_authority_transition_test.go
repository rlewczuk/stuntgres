package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
)

// Ordinary table owner in a separately owned namespace: USAGE and CREATE are
// independent host grants, so CREATE can be revoked without hiding the source.
func renameAuthorityFixture(t *testing.T) (*Engine, *session.Session, *session.Session, ids.ObjectRef, authorization.ACLValue) {
	t.Helper()
	e := New()
	admin, owner := sessionStart(t, e, nil), sessionStart(t, e, nil)
	sessionSQL(t, admin, "CREATE USER rename_transition_owner")
	sessionSQL(t, admin, "CREATE SCHEMA protected")
	actor, _ := e.Roles().Lookup("rename_transition_owner")
	view := e.CatalogRead()
	address := view.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "protected"})
	ns := e.Catalog.Domain.Ref(view.Epoch(), address)
	acl := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: view.MustNamespace(address).Owner, Grantee: authorization.ACLGrantee{Role: actor.Ref}, Privileges: authorization.UsagePrivilege | authorization.CreatePrivilege}}}
	if err := e.SetObjectACL(context.Background(), ns, acl); err != nil {
		t.Fatal(err)
	}
	sessionSQL(t, owner, "SET SESSION AUTHORIZATION rename_transition_owner")
	sessionSQL(t, owner, "CREATE TABLE protected.t(a INT CONSTRAINT nn NOT NULL,b TEXT)")
	sessionSQL(t, owner, "INSERT INTO protected.t VALUES(7,'keep')")
	return e, admin, owner, ns, acl
}
