package viewcmds_test

import (
	"context"
	"database/sql/driver"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func TestViewReplaceOwnerTransferAuthority(t *testing.T) {
	for _, set := range []bool{false, true} {
		t.Run(map[bool]string{false: "no_set", true: "set"}[set], func(t *testing.T) {
			e := engine.New()
			op, done := enginetest.RoleOperation(t, e, "postgres")
			defer done()
			for _, q := range []string{"CREATE USER owner", "CREATE USER successor", "CREATE SCHEMA s AUTHORIZATION owner", "SET SESSION AUTHORIZATION owner", "CREATE TABLE s.t(id INT)", "INSERT INTO s.t VALUES(7)", "CREATE VIEW s.v AS SELECT id FROM s.t", "RESET SESSION AUTHORIZATION"} {
				enginetest.RoleExecute(t, e, op, q)
			}
			owner, _ := e.Roles().Lookup("owner")
			successor, _ := e.Roles().Lookup("successor")
			admin := e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
			ns := e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), e.CatalogRead().Lookup(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "s"})[0])
			if err := enginetest.SetObjectACL(e, context.Background(), ns, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner.Ref, Grantee: authorization.ACLGrantee{Role: owner.Ref}, Privileges: authorization.UsagePrivilege | authorization.CreatePrivilege}, {Grantor: owner.Ref, Grantee: authorization.ACLGrantee{Role: successor.Ref}, Privileges: authorization.UsagePrivilege | authorization.CreatePrivilege}}}); err != nil {
				t.Fatal(err)
			}
			if err := grantMembership(t, e, authorization.Membership{Role: successor.Ref, Member: owner.Ref, Grantor: admin, Inherit: true, Set: set}); err != nil {
				t.Fatal(err)
			}
			ref := enginetest.RoleTableRef(t, e, "s.v")
			enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION owner")
			before := e.Catalog.State
			_, err := e.Execute(op, enginetest.Statement(t, "ALTER VIEW s.v OWNER TO successor"), 0, nil)
			if !set {
				viewReplaceError(t, err, sqlstate.Error{Code: "42501", Message: "must be able to SET ROLE \"successor\""})
				if e.Catalog.State != before {
					t.Fatal("failed transfer published")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if e.Catalog.State.MustRelation(ref.Address).Owner != successor.Ref {
				t.Fatal("transfer changed identity")
			}
			enginetest.RoleExecute(t, e, op, "RESET SESSION AUTHORIZATION")
			_, err = enginetest.Execute(e, context.Background(), enginetest.Statement(t, "SELECT id FROM s.v"), nil)
			viewReplaceError(t, err, sqlstate.Error{Code: "42501", Message: "permission denied for table t"})
			if err := enginetest.SetObjectACL(e, context.Background(), enginetest.RoleTableRef(t, e, "s.t"), authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner.Ref, Grantee: authorization.ACLGrantee{Role: successor.Ref}, Privileges: authorization.SelectPrivilege}}}); err != nil {
				t.Fatal(err)
			}
			if got := enginetest.Tuples(enginetest.Run(t, e, "SELECT id FROM s.v")); !reflect.DeepEqual(got, [][]driver.Value{{int64(7)}}) {
				t.Fatal(got)
			}
		})
	}
}

func TestViewReplaceSchemaOwnerDrop(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{"CREATE USER view_owner", "CREATE USER schema_owner", "CREATE SCHEMA s AUTHORIZATION view_owner", "SET SESSION AUTHORIZATION view_owner", "CREATE TABLE s.t(id INT)", "CREATE VIEW s.v AS SELECT id FROM s.t", "RESET SESSION AUTHORIZATION", "ALTER SCHEMA s OWNER TO schema_owner", "SET SESSION AUTHORIZATION schema_owner"} {
		enginetest.RoleExecute(t, e, op, q)
	}
	for _, q := range []string{"ALTER VIEW s.v SET(security_barrier=true)", "CREATE OR REPLACE VIEW s.v AS SELECT id FROM s.t"} {
		before := e.Catalog.State
		_, err := e.Execute(op, enginetest.Statement(t, q), 0, nil)
		viewReplaceError(t, err, sqlstate.Error{Code: "42501", Message: "must be owner of view v"})
		if e.Catalog.State != before {
			t.Fatal("schema ownership widened ALTER rights")
		}
	}
	ref := enginetest.RoleTableRef(t, e, "s.v")
	enginetest.RoleExecute(t, e, op, "DROP VIEW s.v")
	if _, exists := e.CatalogRead().Descriptor(ref.Address); exists {
		t.Fatal("schema-owner DROP retained view")
	}
}

// Fixtures use the real private command planner, with checked identities and
// explicit options; there is no second append-only membership publication path.
func grantMembership(t *testing.T, e *core.Engine, m authorization.Membership) error {
	t.Helper()
	r := e.Roles()
	names := make([]string, 3)
	for i, ref := range []authorization.PrincipalRef{m.Role, m.Member, m.Grantor} {
		d, err := r.Descriptor(ref)
		if err != nil {
			return err
		}
		names[i] = d.Name
	}
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	spec := func(name string) parser.RoleSpec { return parser.RoleSpec{Name: name} }
	g := spec(names[2])
	stmt := &parser.RoleMembership{Targets: []parser.RoleSpec{spec(names[0])}, Members: []parser.RoleSpec{spec(names[1])}, Grantor: &g, Options: []parser.MembershipOption{{Name: "admin", Value: m.Admin}, {Name: "inherit", Value: m.Inherit}, {Name: "set", Value: m.Set}}}
	_, err := e.Execute(op, stmt, 0, nil)
	return err
}
