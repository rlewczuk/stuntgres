package engine

import (
	"bytes"
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"strings"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func roleExecute(t *testing.T, e *Engine, op *Operation, sql string) Result {
	t.Helper()
	r, err := e.Execute(op, statement(t, sql), 0, nil)
	if err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	if r.Rows != nil {
		t.Cleanup(func() {
			if err := r.Rows.Close(); err != nil {
				t.Error(err)
			}
		})
	}
	return r
}
func roleError(t *testing.T, e *Engine, op *Operation, sql, code, message, detail string) {
	t.Helper()
	roleErrorPosition(t, e, op, sql, code, message, detail, "")
}
func roleErrorPosition(t *testing.T, e *Engine, op *Operation, sql, code, message, detail, position string) {
	t.Helper()
	_, err := e.Execute(op, statement(t, sql), 0, nil)
	var d *sqlstate.Error
	if !errors.As(err, &d) {
		t.Fatalf("%s: %T %v", sql, err, err)
	}
	want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Detail: detail, Position: position}
	if !reflect.DeepEqual(d, want) {
		t.Fatalf("%s: %#v want %#v", sql, d, want)
	}
}
func roleOperation(t *testing.T, e *Engine, name string) (*Operation, func()) {
	t.Helper()
	ref, err := e.ResolveLogin(name)
	if err != nil {
		t.Fatal(err)
	}
	s, err := e.StartSession(context.Background(), SessionStart{ID: ids.SessionID(testSessionID.Add(1)), Authenticated: ref})
	if err != nil {
		t.Fatal(err)
	}
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return op, func() {
		op.Done()
		s.RequestEnd(CleanClose)
		if err := s.WaitEnd(context.Background()); err != nil {
			t.Error(err)
		}
	}
}
func roleIdentity(t *testing.T, e *Engine, op *Operation, want string) {
	t.Helper()
	r := roleExecute(t, e, op, "SELECT session_user,current_user")
	values := tuples(r)
	if r.Count != 1 || !reflect.DeepEqual(values, [][]driver.Value{{want, want}}) || r.Rows.Schema()[0].Usage.Type.ID() != sqltype.Name || r.Rows.Schema()[1].Usage.Type.ID() != sqltype.Name {
		t.Fatalf("identity: %#v %#v", values, r.Rows.Schema())
	}
}

func TestRoleAuthorityAndSurface(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	old := e.Roles()
	for _, sql := range []string{"CREATE ROLE a", "CREATE USER b", "CREATE GROUP c", "CREATE ROLE d WITH LOGIN", "CREATE USER f NOLOGIN"} {
		if r := roleExecute(t, e, op, sql); r.Count != 0 {
			t.Fatal(r)
		}
	}
	for name, login := range map[string]bool{"a": false, "b": true, "c": false, "d": true, "f": false} {
		d, ok := e.Roles().Lookup(name)
		if !ok || d.Login != login || d.Ref.ID() == 0 {
			t.Fatal(name, d)
		}
	}
	if _, ok := old.Lookup("a"); ok {
		t.Fatal("retained role read mutated")
	}
	a, _ := e.Roles().Lookup("a")
	other := New()
	if _, err := other.Roles().Descriptor(a.Ref); err == nil {
		t.Fatal("foreign principal")
	}
	if _, err := e.Roles().Descriptor(authorization.PrincipalRef{}); err == nil {
		t.Fatal("zero principal")
	}
	roleError(t, e, op, "CREATE ROLE a", "42710", `role "a" already exists`, "")
	roleExecute(t, e, op, "DROP GROUP a,a")
	if _, err := e.Roles().Descriptor(a.Ref); err == nil {
		t.Fatal("stale principal")
	}
	roleExecute(t, e, op, "CREATE ROLE a")
	next, _ := e.Roles().Lookup("a")
	if next.Ref == a.Ref {
		t.Fatal("principal reused")
	}
	roleError(t, e, op, "DROP USER absent", "42704", `role "absent" does not exist`, "")
	roleExecute(t, e, op, "CREATE ROLE Foo")
	roleError(t, e, op, `CREATE ROLE "foo"`, "42710", `role "foo" already exists`, "")
	roleExecute(t, e, op, `CREATE ROLE "Foo"`)
	long := strings.Repeat("a", 62) + "é"
	roleExecute(t, e, op, `CREATE ROLE "`+long+`"`)
	roleError(t, e, op, `CREATE ROLE "`+strings.Repeat("a", 62)+`"`, "42710", `role "`+strings.Repeat("a", 62)+`" already exists`, "")
	for _, query := range []string{"CREATE ROLE nope WITH ADMIN", "CREATE ROLE nope SYSID 12"} {
		if _, err := parser.Parse(query); err == nil {
			t.Fatal(query)
		}
		if _, ok := e.Roles().Lookup("nope"); ok {
			t.Fatal("failed parser published")
		}
	}
	for _, query := range []string{"CREATE ROLE nope LOGIN LOGIN", "CREATE ROLE nope LOGIN NOLOGIN"} {
		if _, err := e.Describe(op, statement(t, query)); err != nil {
			t.Fatal(err)
		}
		_, err := e.Execute(op, statement(t, query), 0, nil)
		state(t, err, "42601")
		if _, ok := e.Roles().Lookup("nope"); ok {
			t.Fatal("duplicate option published")
		}
	}
	roleError(t, e, op, "CREATE ROLE pg_nope", "42939", `role name "pg_nope" is reserved`, `Role names starting with "pg_" are reserved.`)
}

func TestRoleSessionAuthorizationAndStaleIdentity(t *testing.T) {
	e := New()
	admin, done := roleOperation(t, e, "postgres")
	defer done()
	for _, sql := range []string{"CREATE USER a", "CREATE USER b", "CREATE ROLE nolog"} {
		roleExecute(t, e, admin, sql)
	}
	a, closeA := roleOperation(t, e, "a")
	defer closeA()
	b, closeB := roleOperation(t, e, "b")
	defer closeB()
	before := a.RoleState()
	roleError(t, e, a, "SET SESSION AUTHORIZATION b", "42501", `permission denied to set session authorization "b"`, "")
	if a.RoleState() != before {
		t.Fatal("failed switch mutated")
	}
	roleIdentity(t, e, b, "b")
	for _, sql := range []string{"SET SESSION AUTHORIZATION a", "SET SESSION AUTHORIZATION DEFAULT", "RESET SESSION AUTHORIZATION"} {
		roleExecute(t, e, a, sql)
		roleIdentity(t, e, a, "a")
	}
	roleExecute(t, e, admin, "SET SESSION AUTHORIZATION nolog")
	roleIdentity(t, e, admin, "nolog")
	roleExecute(t, e, admin, "SET SESSION AUTHORIZATION a")
	roleExecute(t, e, admin, "SET SESSION AUTHORIZATION b")
	roleExecute(t, e, admin, "RESET SESSION AUTHORIZATION")
	roleIdentity(t, e, admin, "postgres")
	roleError(t, e, admin, "SET SESSION AUTHORIZATION absent", "22023", `role "absent" does not exist`, "")
	roleIdentity(t, e, admin, "postgres")
	roleExecute(t, e, admin, "DROP ROLE a")
	roleExecute(t, e, admin, "CREATE USER a")
	if _, err := e.Execute(a, statement(t, "SELECT current_user"), 0, nil); err == nil {
		t.Fatal("stale session adopted recreated role")
	}
	if _, err := e.StartSession(context.Background(), SessionStart{ID: ids.SessionID(testSessionID.Add(1)), Authenticated: New().roles.domain.Principal(10)}); err == nil {
		t.Fatal("foreign initialization")
	}
	nolog, _ := e.Roles().Lookup("nolog")
	if _, err := e.StartSession(context.Background(), SessionStart{ID: ids.SessionID(testSessionID.Add(1)), Authenticated: nolog.Ref}); err == nil {
		t.Fatal("NOLOGIN admission")
	}
}

func TestRoleDropMembershipAtomicity(t *testing.T) {
	for _, order := range []string{"grantor,endpoint", "endpoint,grantor", "grantor,member", "member,grantor"} {
		t.Run(order, func(t *testing.T) {
			e := New()
			op, done := roleOperation(t, e, "postgres")
			defer done()
			for _, name := range []string{"grantor", "endpoint", "member", "retained"} {
				roleExecute(t, e, op, "CREATE ROLE "+name)
			}
			lookup := func(name string) authorization.PrincipalRef { d, _ := e.Roles().Lookup(name); return d.Ref }
			upstream := authorization.Membership{Role: lookup("endpoint"), Member: lookup("grantor"), Grantor: lookup("postgres"), Admin: true, Inherit: true, Set: true}
			downstream := authorization.Membership{Role: lookup("endpoint"), Member: lookup("member"), Grantor: lookup("grantor"), Inherit: true, Set: true}
			unrelated := authorization.Membership{Role: lookup("retained"), Member: lookup("member"), Grantor: lookup("postgres")}
			seed := e.Roles().Memberships()
			for _, edge := range []authorization.Membership{upstream, downstream, unrelated} {
				if err := grantMembership(t, e, edge); err != nil {
					t.Fatal(err)
				}
			}
			before := e.roles.published
			read := e.Roles()
			roleError(t, e, op, "DROP ROLE endpoint,missing", "42704", `role "missing" does not exist`, "")
			if e.roles.published != before {
				t.Fatal("partial drop")
			}
			roleError(t, e, op, "DROP ROLE grantor", "2BP01", `role "grantor" cannot be dropped because some objects depend on it`, "privileges for membership of role member in role endpoint")
			if e.roles.published != before {
				t.Fatal("hypothetical endpoint cleanup escaped failed drop")
			}
			roleExecute(t, e, op, "DROP ROLE "+order)
			if strings.Contains(order, "member") {
				if len(fixtureEdges(e.roles.published)) != 3 {
					t.Fatal("member batch retained edges", fixtureEdges(e.roles.published))
				}
			} else {
				if len(fixtureEdges(e.roles.published)) != 4 || !reflect.DeepEqual(fixtureEdges(e.roles.published)[:3], seed) || fixtureEdges(e.roles.published)[3] != unrelated {
					t.Fatal("unrelated edge removed", fixtureEdges(e.roles.published))
				}
				roleExecute(t, e, op, "DROP ROLE member")
				if len(fixtureEdges(e.roles.published)) != 3 {
					t.Fatal("member endpoint retained")
				}
			}
			if !reflect.DeepEqual(read.Memberships(), append(seed, upstream, downstream, unrelated)) {
				t.Fatal("retained membership view changed")
			}
			copied := read.Memberships()
			copied[3] = authorization.Membership{}
			if read.Memberships()[3] != upstream {
				t.Fatal("membership read exposed backing array")
			}
		})
	}
}

func TestRolePersistentReferencesAndIdentityGuards(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	roleExecute(t, e, op, "CREATE ROLE dependent")
	d, _ := e.Roles().Lookup("dependent")
	payload := []authorization.PrincipalRef{d.Ref}
	if err := e.SetRoleReferences(context.Background(), "feature payload", payload); err != nil {
		t.Fatal(err)
	}
	payload[0] = authorization.PrincipalRef{}
	before := e.roles.published
	roleError(t, e, op, "DROP ROLE dependent", "2BP01", `role "dependent" cannot be dropped because some objects depend on it`, "privileges for feature payload")
	if e.roles.published != before {
		t.Fatal("persistent dependency failure mutated roles")
	}
	if err := e.SetRoleReferences(context.Background(), "feature payload", nil); err != nil {
		t.Fatal(err)
	}
	op.Session.Mu.Lock()
	op.Session.Authorization.State.SessionUser = d.Ref
	op.Session.Mu.Unlock()
	roleError(t, e, op, "DROP ROLE dependent", "55006", "session user cannot be dropped", "")
	op.Session.Mu.Lock()
	op.Session.Authorization.State.SessionUser = op.Session.Authorization.State.Authenticated
	op.Session.Mu.Unlock()
	roleError(t, e, op, "DROP ROLE postgres", "55006", "current user cannot be dropped", "")
	roleExecute(t, e, op, "DROP ROLE dependent")
	if err := grantMembership(t, e, authorization.Membership{Role: d.Ref}); err == nil {
		t.Fatal("stale membership endpoints")
	}
	if err := e.SetRoleReferences(context.Background(), "feature payload", []authorization.PrincipalRef{d.Ref}); err == nil {
		t.Fatal("stale persistent reference")
	}
}

func TestRoleDumpRestorePreservesLiveIdentities(t *testing.T) {
	e := New()
	admin, done := roleOperation(t, e, "postgres")
	defer done()
	for _, query := range []string{`CREATE USER "a'quote"`, `CREATE USER "b\slash"`} {
		roleExecute(t, e, admin, query)
	}
	roleExecute(t, e, admin, `SET SESSION AUTHORIZATION "b\slash"`)
	withPublicSchemaUnitOwner(t, e, `b\slash`, func() {
		roleExecute(t, e, admin, "CREATE TABLE first(id INT)")
	})
	roleExecute(t, e, admin, "INSERT INTO first VALUES(7)")
	roleExecute(t, e, admin, `SET SESSION AUTHORIZATION "a'quote"`)
	withPublicSchemaUnitOwner(t, e, "a'quote", func() {
		roleExecute(t, e, admin, "CREATE TABLE second(id INT)")
	})
	roleExecute(t, e, admin, "INSERT INTO second VALUES(8)")
	peer, closePeer := roleOperation(t, e, `b\slash`)
	defer closePeer()
	beforeAdmin, beforePeer := admin.RoleState(), peer.RoleState()
	var dump bytes.Buffer
	if err := e.Dump(context.Background(), &dump); err != nil {
		t.Fatal(err)
	}
	roles := e.roles.published
	if err := e.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := e.Restore(context.Background(), strings.NewReader(dump.String())); err != nil {
		t.Fatal(err)
	}
	if admin.RoleState() != beforeAdmin || peer.RoleState() != beforePeer || e.roles.published != roles {
		t.Fatal("controls changed cluster/session identity")
	}
	roleIdentity(t, e, admin, "a'quote")
	roleIdentity(t, e, peer, `b\slash`)
	roleExecute(t, e, admin, "SELECT id FROM second")
	roleExecute(t, e, peer, "SELECT id FROM first")
	owners := make(map[string]authorization.PrincipalRef, 2)
	for _, name := range []string{"first", "second"} {
		ref := roleTableRef(t, e, name)
		d := e.catalog.state.MustRelation(ref.Address)
		if e.Roles().ValidatePrincipal(d.Owner) != nil {
			t.Fatal("foreign owner after restore")
		}
		owners[name] = d.Owner
	}
	// All reset spellings share the existing normalized administrative-reset
	// AST. They reset only Restore's isolated session, never the live callers.
	for _, query := range []string{"RESET SESSION AUTHORIZATION", "SET SESSION AUTHORIZATION DEFAULT", "SET AUTHORIZATION DEFAULT"} {
		if err := e.Restore(context.Background(), strings.NewReader(dump.String()+query+"; CREATE TABLE reset_owner_marker(id INT);")); err != nil {
			t.Fatal("restore administrative reset", query, err)
		}
		if admin.RoleState() != beforeAdmin || peer.RoleState() != beforePeer || e.roles.published != roles {
			t.Fatal("isolated reset changed live role authority", query)
		}
		marker := roleTableRef(t, e, "reset_owner_marker")
		if e.catalog.state.MustRelation(marker.Address).Owner != e.roles.domain.Principal(10) {
			t.Fatal("reset did not restore isolated authenticated owner", query)
		}
		for name, owner := range owners {
			ref := roleTableRef(t, e, name)
			d := e.catalog.state.MustRelation(ref.Address)
			if d.Owner != owner || d.Security.ACL.Explicit || len(d.Security.ColumnACL) != 0 {
				t.Fatal("reset changed restored table authority", name, query)
			}
		}
	}
	before := e.catalog.state
	for _, query := range []string{"SET AUTHORIZATION 'postgres'", "CREATE ROLE forbidden_restore_role", `ALTER ROLE "a'quote" NOLOGIN`, `DROP ROLE "b\slash"`} {
		err := e.Restore(context.Background(), strings.NewReader(dump.String()+"SET SESSION AUTHORIZATION DEFAULT; CREATE TABLE unpublished(id INT); "+query+";"))
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" {
			t.Fatal("restore accepted forbidden owner mutation", query, err)
		}
		if e.catalog.state != before || e.roles.published != roles || admin.RoleState() != beforeAdmin || peer.RoleState() != beforePeer {
			t.Fatal("rejected directive changed catalog or role authority")
		}
	}
}
