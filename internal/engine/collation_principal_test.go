package engine

import (
	"context"
	"database/sql/driver"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// The baseline has no public role-rename SQL. This fixture follows the existing
// immutable role publication convention without changing any principal identity,
// memberships, attributes, catalog owners, or collation properties.
func collationRenameBootstrapFixture(e *Engine, op *Operation, name string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	auth, err := e.authorizationLocked(op)
	if err != nil {
		return err
	}
	if err = op.Context().Err(); err != nil {
		return err
	}
	actor, err := auth.Roles.Descriptor(auth.State.CurrentUser)
	if err != nil {
		return err
	}
	if !actor.Superuser {
		return fmt.Errorf("bootstrap rename fixture requires actual superuser authority")
	}
	ref := e.roles.domain.Principal(10)
	d, err := auth.Roles.Descriptor(ref)
	if err != nil {
		return err
	}
	if name == "" {
		return fmt.Errorf("empty fixture role name")
	}
	if _, exists := auth.Roles.Lookup(name); exists {
		return fmt.Errorf("fixture role name already exists")
	}
	if e.roles.published.Revision == ^uint64(0) {
		return fmt.Errorf("authorization revision exhausted")
	}
	n := cloneRoleFixture(e.roles.published)
	d.Name = name
	putFixtureRole(n, d)

	op.session.mu.Lock()
	defer op.session.mu.Unlock()
	if err = publicationOperationLocked(e, op); err != nil {
		return err
	}
	e.roles.published = n
	return nil
}

func TestCollationBootstrapPrincipalNameReuse(t *testing.T) {
	e := New()
	admin, done := roleOperation(t, e, "postgres")
	defer done()
	bootstrap := e.roles.domain.Principal(10)
	beforeRolesPublication := e.roles.published
	beforeRoles := e.Roles()
	beforeDescriptor, err := beforeRoles.Descriptor(bootstrap)
	if err != nil {
		t.Fatal(err)
	}
	beforeCatalog := e.CatalogRead()
	roleExecute(t, e, admin, `CREATE COLLATION coll_bootstrap_capture FROM pg_catalog."C"`)
	if err = collationRenameBootstrapFixture(e, admin, "original_bootstrap"); err != nil {
		t.Fatal(err)
	}
	actual, err := e.Roles().Descriptor(bootstrap)
	if err != nil {
		t.Fatal(err)
	}
	want := beforeDescriptor
	want.Name = "original_bootstrap"
	if actual != want || e.roles.published.Revision != beforeRolesPublication.Revision+1 || !reflect.DeepEqual(fixtureEdges(e.roles.published), fixtureEdges(beforeRolesPublication)) {
		t.Fatal("rename changed genuine role identity", actual, want)
	}
	old, err := beforeRoles.Descriptor(bootstrap)
	if err != nil || old != beforeDescriptor {
		t.Fatal("published rename mutated captured RoleRead", old, err)
	}
	if d, ok := beforeRoles.Lookup("postgres"); !ok || d.Ref != bootstrap {
		t.Fatal("old role name index lost snapshot")
	}
	roleExecute(t, e, admin, `CREATE USER postgres`)
	replacement, ok := e.Roles().Lookup("postgres")
	if !ok || replacement.Ref == bootstrap || replacement.Superuser || !replacement.Login {
		t.Fatal("replacement was not allocated ordinary role", replacement)
	}
	roleIdentity(t, e, admin, "original_bootstrap")
	assertOwner := func(name string) {
		t.Helper()
		r := roleExecute(t, e, admin, `SELECT collowner::bigint AS owner FROM pg_catalog.pg_collation WHERE collname='`+name+`'`)
		if got := tuples(r); !reflect.DeepEqual(got, [][]driver.Value{{int64(bootstrap.ID())}}) {
			t.Fatal("capture followed reused role name", name, got)
		}
	}
	assertOwner("C")
	assertOwner("coll_bootstrap_capture")
	for _, view := range []catalog.Read{beforeCatalog, e.CatalogRead()} {
		d, _ := view.Descriptor(ids.ObjectAddress{ClassID: ids.CollationClassID, ObjectID: 950})
		if d.(*catalog.Collation).Owner != bootstrap {
			t.Fatal("seed ownership mutated", d)
		}
	}
	ordinary, ordinaryDone := roleOperation(t, e, "postgres")
	defer ordinaryDone()
	roleIdentity(t, e, ordinary, "postgres")
	roleError(t, e, ordinary, `ALTER COLLATION pg_catalog."C" REFRESH VERSION`, "42501", `must be owner of collation C`, "")
	if err = e.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertOwner("C")
	if err = e.Restore(context.Background(), strings.NewReader(`CREATE COLLATION coll_bootstrap_restored FROM pg_catalog."C";`)); err != nil {
		t.Fatal(err)
	}
	assertOwner("C")
	assertOwner("coll_bootstrap_restored")
	roleError(t, e, ordinary, `ALTER COLLATION coll_bootstrap_restored REFRESH VERSION`, "42501", `must be owner of collation coll_bootstrap_restored`, "")
	roleExecute(t, e, admin, `CREATE COLLATION pg_temp.coll_bootstrap_temporary FROM pg_catalog."C"`)
	roleExecute(t, e, admin, `CREATE TABLE coll_bootstrap_survivor(id INT,v TEXT COLLATE pg_temp.coll_bootstrap_temporary DEFAULT ('a' COLLATE pg_temp.coll_bootstrap_temporary))`)
	roleExecute(t, e, admin, `INSERT INTO coll_bootstrap_survivor VALUES(7,'é')`)
	done()
	inspection, err := e.StartSession(context.Background(), SessionStart{ID: ids.SessionID(testSessionID.Add(1)), Authenticated: bootstrap})
	if err != nil {
		t.Fatal(err)
	}
	reader, err := inspection.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		reader.Done()
		inspection.RequestEnd(CleanClose)
		if err := inspection.WaitEnd(context.Background()); err != nil {
			t.Error(err)
		}
	}()
	r := roleExecute(t, e, reader, `SELECT * FROM coll_bootstrap_survivor`)
	if got := tuples(r); !reflect.DeepEqual(got, [][]driver.Value{{int64(7)}}) || len(r.Rows.Schema()) != 1 {
		t.Fatal("private genuine-bootstrap cleanup failed", got, r.Rows.Schema())
	}
	r = roleExecute(t, e, reader, `SELECT COUNT(*) FROM pg_catalog.pg_collation WHERE collname='coll_bootstrap_temporary'`)
	if got := tuples(r); !reflect.DeepEqual(got, [][]driver.Value{{int64(0)}}) {
		t.Fatal("temporary collation survived physical end", got)
	}
	reader.Done()
	inspection.RequestEnd(CleanClose)
	if err := inspection.WaitEnd(context.Background()); err != nil {
		t.Fatal(err)
	}
	e.sessionMu.Lock()
	live := len(e.sessions)
	e.sessionMu.Unlock()
	if live != 1 {
		t.Fatal("private cleanup leaked genuine session", live)
	}
}
