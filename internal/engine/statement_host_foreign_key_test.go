package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
)

// The FK action authority slot round-trips through the host: an empty slot
// reads back as an untyped nil, and each swap returns the authority it
// replaced.
func TestStatementHostForeignActionAuthoritySwap(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE TABLE t(v INT)")
	scope := foreignActionUnitScope(t, e, s, "INSERT INTO t VALUES(1)")
	h := scope.WriteHost()
	auth, err := h.ForeignKeyAuthority()
	if err != nil {
		t.Fatal(err)
	}
	if previous := h.SwapForeignActionAuthority(auth); previous != nil {
		t.Fatalf("empty slot returned %#v", previous)
	}
	if scope.ForeignActionAuth != foreignAuthorizationOf(auth) {
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
	e := New()
	s := sessionStart(t, e, nil)
	for _, q := range []string{"CREATE TABLE t(v INT)", "CREATE ROLE fk_owner", "ALTER TABLE t OWNER TO fk_owner"} {
		sessionSQL(t, s, q)
	}
	owner := e.CatalogRead().MustRelation(catalogRef(t, e, e.CatalogRead(), "t").Address).Owner
	scope := foreignActionUnitScope(t, e, s, "INSERT INTO t VALUES(1)")
	auth, err := scope.WriteHost().ForeignKeyAuthority()
	if err != nil {
		t.Fatal(err)
	}
	if auth.ActingAs(authorization.PrincipalRef{}) != auth {
		t.Fatal("zero owner changed the authority")
	}
	base := foreignAuthorizationOf(auth)
	user := base.State.CurrentUser
	if owner == user {
		t.Fatal("table owner is the session user")
	}
	acting := foreignAuthorizationOf(auth.ActingAs(owner))
	if acting == base || acting.State.CurrentUser != owner || base.State.CurrentUser != user {
		t.Fatal("owner substitution was not a copy", acting.State.CurrentUser, base.State.CurrentUser)
	}
	if acting.Access().Catalog != base.Access().Catalog {
		t.Fatal("owner substitution changed the catalog")
	}
}

// ForeignKeyJobs captures nothing when no staged relation takes part in a
// foreign key, and row-exclusive mode for a cascading child.
func TestStatementHostForeignKeyJobs(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	for _, q := range []string{"CREATE TABLE p(id INT PRIMARY KEY)", "CREATE TABLE c(pid INT REFERENCES p ON UPDATE CASCADE)", "CREATE TABLE unrelated(id INT)", "INSERT INTO p VALUES(1)", "INSERT INTO c VALUES(1)"} {
		sessionSQL(t, s, q)
	}
	scope := foreignActionUnitScope(t, e, s, "INSERT INTO unrelated VALUES(1)")
	jobs, modes, auth, err := scope.WriteHost().ForeignKeyJobs()
	if err != nil || len(jobs) != 0 || modes != nil || auth == nil {
		t.Fatal("irrelevant stage captured jobs", jobs, modes, auth, err)
	}
	scope.Finish(context.Canceled)

	scope = foreignActionUnitScope(t, e, s, "UPDATE p SET id=2")
	jobs, modes, auth, err = scope.WriteHost().ForeignKeyJobs()
	if err != nil {
		t.Fatal(err)
	}
	job := foreignActionUnitJob(t, jobs, "c")
	epoch := auth.Access().Catalog.Epoch()
	child := e.Catalog.Domain.Ref(epoch, job.Child.Address())
	parent := e.Catalog.Domain.Ref(epoch, job.Parent.Address())
	if len(jobs) != 1 || len(modes) != 2 || modes[child] != lock.RowExclusive || modes[parent] != lock.RowShare {
		t.Fatal("cascade modes", jobs, modes)
	}
}

// RetainedForeignKeyRows is empty under READ COMMITTED and borrows the
// snapshot's committed image under REPEATABLE READ.
func TestStatementHostRetainedForeignKeyRows(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	for _, q := range []string{"CREATE TABLE p(id INT PRIMARY KEY)", "CREATE TABLE c(pid INT REFERENCES p ON UPDATE CASCADE)", "INSERT INTO p VALUES(1)", "INSERT INTO c VALUES(1)"} {
		sessionSQL(t, s, q)
	}
	ref := catalogRef(t, e, e.CatalogRead(), "c")
	scope := foreignActionUnitScope(t, e, s, "UPDATE p SET id=2")
	h := scope.WriteHost()
	current, err := h.ForeignKeyRows(ref, ids.ObjectAddress{})
	if err != nil {
		t.Fatal(err)
	}
	retained, err := h.RetainedForeignKeyRows(ref, current)
	if err != nil || retained.Storage != nil {
		t.Fatal("READ COMMITTED retained an image", retained.Storage, err)
	}
	scope.Finish(context.Canceled)

	sessionSQL(t, s, "BEGIN ISOLATION LEVEL REPEATABLE READ")
	sessionSQL(t, s, "SELECT * FROM c")
	scope = foreignActionUnitScope(t, e, s, "UPDATE p SET id=3")
	h = scope.WriteHost()
	current, err = h.ForeignKeyRows(ref, ids.ObjectAddress{})
	if err != nil {
		t.Fatal(err)
	}
	retained, err = h.RetainedForeignKeyRows(ref, current)
	root := scope.Owner.Roots[ref.Address]
	if err != nil || retained.Storage != current.Storage || len(retained.Committed) != len(root.Versions) || retained.Frontier != scope.Owner.Frontier {
		t.Fatal("REPEATABLE READ image", retained, err)
	}
	var _ dml.FinishHost = h
}
