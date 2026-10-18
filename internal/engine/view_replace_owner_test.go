package engine

import (
	"context"
	"database/sql/driver"
	"reflect"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestViewReplaceOwnerTransferAuthority(t *testing.T) {
	for _, set := range []bool{false, true} {
		t.Run(map[bool]string{false: "no_set", true: "set"}[set], func(t *testing.T) {
			e := New()
			op, done := roleOperation(t, e, "postgres")
			defer done()
			for _, q := range []string{"CREATE USER owner", "CREATE USER successor", "CREATE SCHEMA s AUTHORIZATION owner", "SET SESSION AUTHORIZATION owner", "CREATE TABLE s.t(id INT)", "INSERT INTO s.t VALUES(7)", "CREATE VIEW s.v AS SELECT id FROM s.t", "RESET SESSION AUTHORIZATION"} {
				roleExecute(t, e, op, q)
			}
			owner, _ := e.Roles().Lookup("owner")
			successor, _ := e.Roles().Lookup("successor")
			admin := e.roles.root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
			ns := e.catalog.Domain.Ref(e.catalog.state.epoch, e.CatalogRead().Lookup(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "s"})[0])
			if err := e.SetObjectACL(context.Background(), ns, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner.Ref, Grantee: authorization.ACLGrantee{Role: owner.Ref}, Privileges: authorization.UsagePrivilege | authorization.CreatePrivilege}, {Grantor: owner.Ref, Grantee: authorization.ACLGrantee{Role: successor.Ref}, Privileges: authorization.UsagePrivilege | authorization.CreatePrivilege}}}); err != nil {
				t.Fatal(err)
			}
			if err := grantMembership(t, e, authorization.Membership{Role: successor.Ref, Member: owner.Ref, Grantor: admin, Inherit: true, Set: set}); err != nil {
				t.Fatal(err)
			}
			ref := roleTableRef(t, e, "s.v")
			roleExecute(t, e, op, "SET SESSION AUTHORIZATION owner")
			before := e.catalog.state
			_, err := e.Execute(op, statement(t, "ALTER VIEW s.v OWNER TO successor"), 0, nil)
			if !set {
				viewReplaceError(t, err, sqlstate.Error{Code: "42501", Message: "must be able to SET ROLE \"successor\""})
				if e.catalog.state != before {
					t.Fatal("failed transfer published")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if e.catalog.state.MustRelation(ref.Address).Owner != successor.Ref {
				t.Fatal("transfer changed identity")
			}
			roleExecute(t, e, op, "RESET SESSION AUTHORIZATION")
			_, err = testExecute(e, context.Background(), statement(t, "SELECT id FROM s.v"), nil)
			viewReplaceError(t, err, sqlstate.Error{Code: "42501", Message: "permission denied for table t"})
			if err := e.SetObjectACL(context.Background(), roleTableRef(t, e, "s.t"), authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner.Ref, Grantee: authorization.ACLGrantee{Role: successor.Ref}, Privileges: authorization.SelectPrivilege}}}); err != nil {
				t.Fatal(err)
			}
			if got := tuples(run(t, e, "SELECT id FROM s.v")); !reflect.DeepEqual(got, [][]driver.Value{{int64(7)}}) {
				t.Fatal(got)
			}
		})
	}
}

func TestViewReplaceSchemaOwnerDrop(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{"CREATE USER view_owner", "CREATE USER schema_owner", "CREATE SCHEMA s AUTHORIZATION view_owner", "SET SESSION AUTHORIZATION view_owner", "CREATE TABLE s.t(id INT)", "CREATE VIEW s.v AS SELECT id FROM s.t", "RESET SESSION AUTHORIZATION", "ALTER SCHEMA s OWNER TO schema_owner", "SET SESSION AUTHORIZATION schema_owner"} {
		roleExecute(t, e, op, q)
	}
	for _, q := range []string{"ALTER VIEW s.v SET(security_barrier=true)", "CREATE OR REPLACE VIEW s.v AS SELECT id FROM s.t"} {
		before := e.catalog.state
		_, err := e.Execute(op, statement(t, q), 0, nil)
		viewReplaceError(t, err, sqlstate.Error{Code: "42501", Message: "must be owner of view v"})
		if e.catalog.state != before {
			t.Fatal("schema ownership widened ALTER rights")
		}
	}
	ref := roleTableRef(t, e, "s.v")
	roleExecute(t, e, op, "DROP VIEW s.v")
	if _, exists := e.CatalogRead().Descriptor(ref.Address); exists {
		t.Fatal("schema-owner DROP retained view")
	}
}
