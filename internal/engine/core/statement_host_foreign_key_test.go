package core_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
)

// The FK action authority slot round-trips through the host: an empty slot
// reads back as an untyped nil, and each swap returns the authority it
// replaced.
func TestStatementHostForeignActionAuthoritySwap(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE TABLE t(v INT)")
	scope := foreignActionUnitScope(t, e, s, "INSERT INTO t VALUES(1)")
	h := scope.WriteHost()
	auth, err := h.ForeignKeyAuthority()
	if err != nil {
		t.Fatal(err)
	}
	if previous := h.SwapForeignActionAuthority(auth); previous != nil {
		t.Fatalf("empty slot returned %#v", previous)
	}
	if scope.ForeignActionAuth != core.ForeignAuthorizationOf(auth) {
		t.Fatal("swap did not install the authority")
	}
	if previous := h.SwapForeignActionAuthority(nil); previous != auth {
		t.Fatal("swap did not return the installed authority", previous)
	}
	if scope.ForeignActionAuth != nil {
		t.Fatal("nil swap did not clear the slot")
	}
}

// ActingAs with no owner is the receiver; with an owner it is a copy running
// as that owner, and the receiver keeps its user.
func TestStatementHostForeignAuthorityActingAs(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	for _, q := range []string{"CREATE TABLE t(v INT)", "CREATE ROLE fk_owner", "ALTER TABLE t OWNER TO fk_owner"} {
		enginetest.SessionSQL(t, s, q)
	}
	owner := e.CatalogRead().MustRelation(enginetest.CatalogRef(t, e, e.CatalogRead(), "t").Address).Owner
	scope := foreignActionUnitScope(t, e, s, "INSERT INTO t VALUES(1)")
	auth, err := scope.WriteHost().ForeignKeyAuthority()
	if err != nil {
		t.Fatal(err)
	}
	if auth.ActingAs(authorization.PrincipalRef{}) != auth {
		t.Fatal("zero owner changed the authority")
	}
	base := core.ForeignAuthorizationOf(auth)
	user := base.State.CurrentUser
	if owner == user {
		t.Fatal("table owner is the session user")
	}
	acting := core.ForeignAuthorizationOf(auth.ActingAs(owner))
	if acting == base || acting.State.CurrentUser != owner || base.State.CurrentUser != user {
		t.Fatal("owner substitution was not a copy", acting.State.CurrentUser, base.State.CurrentUser)
	}
	if acting.Access().Catalog != base.Access().Catalog {
		t.Fatal("owner substitution changed the catalog")
	}
}
