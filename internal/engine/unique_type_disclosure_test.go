package engine

import (
	"context"
	"database/sql/driver"
	"reflect"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/sqlstate"
	"testing"
)

// SQL GRANT/REVOKE is excluded natively. Real catalog ACL mutation supplies the
// independent counterpart of the pinned owner-REVOKE PostgreSQL control.
func TestUniqueTypeBuildOwnerDisclosure(t *testing.T) {
	for _, mode := range []string{"none", "partial", "all", "table"} {
		for _, prepared := range []bool{false, true} {
			t.Run(mode+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				e := New()
				admin, adminDone := roleOperation(t, e, "postgres")
				defer adminDone()
				roleExecute(t, e, admin, "CREATE ROLE unique_type_owner LOGIN")
				owner, ownerDone := roleOperation(t, e, "unique_type_owner")
				defer ownerDone()
				withPublicSchemaUnitOwner(t, e, "unique_type_owner", func() {
					roleExecute(t, e, owner, "CREATE TABLE t(a INT,b TEXT,CONSTRAINT u UNIQUE(a,b))")
				})
				roleExecute(t, e, owner, "INSERT INTO t VALUES(1,'protected'),(2,'protected')")
				ref := roleTableRef(t, e, "t")
				actor, ok := e.Roles().Lookup("unique_type_owner")
				if !ok || actor.Superuser {
					t.Fatal("owner fixture must be nonsuperuser", actor, ok)
				}
				grant := func(attribute int32, privileges authorization.Privileges) {
					target := ref
					target.Address.SubID = attribute
					acl := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: actor.Ref, Grantee: authorization.ACLGrantee{Role: actor.Ref}, Privileges: privileges}}}
					if err := e.SetObjectACL(context.Background(), target, acl); err != nil {
						t.Fatal(err)
					}
				}
				tableRights := authorization.InsertPrivilege
				if mode == "table" {
					tableRights |= authorization.SelectPrivilege
				}
				grant(0, tableRights)
				if mode == "partial" || mode == "all" {
					grant(1, authorization.SelectPrivilege)
				}
				if mode == "all" {
					grant(2, authorization.SelectPrivilege)
				}
				before := e.catalog.State
				parent := before.MustRelation(ref.Address)
				constraint := parent.Constraints.Unique[0]
				root := e.storage.IndexRoots[constraint.Index]
				store := e.storage.Relations[ref.Address]
				execute := func(query string) (Result, error) {
					ast := statement(t, query)
					if !prepared {
						return e.Execute(owner, ast, 0, nil)
					}
					scope, err := e.StartStatement(owner)
					if err != nil {
						return Result{}, err
					}
					description, err := scope.Describe(ast)
					if err != nil {
						scope.Finish(err)
						return Result{}, err
					}
					scope.Expected = &description
					result, err := scope.Execute(ast, 0, nil)
					scope.Finish(err)
					return result, err
				}
				result, err := execute("ALTER TABLE t ALTER a TYPE BIGINT USING 0")
				if result.Count != 0 || result.Rows != nil {
					t.Fatal("failed TYPE returned output", result)
				}
				detail := "Duplicate keys exist."
				if mode == "all" || mode == "table" {
					detail = "Key (a, b)=(0, protected) is duplicated."
				}
				nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "23505", Message: `could not create unique index "u"`, Detail: detail, Schema: "public", Table: "t", Constraint: "u"})
				if e.catalog.State != before || e.storage.Relations[ref.Address] != store || e.storage.IndexRoots[constraint.Index] != root {
					t.Fatal("failed TYPE changed catalog/storage/root identity")
				}
				current := e.catalog.State.MustRelation(ref.Address)
				if current.Constraints.Unique[0].OID != constraint.OID || current.Constraints.Unique[0].Index != constraint.Index {
					t.Fatal("failed TYPE reminted identities")
				}
				wantRows := [][]driver.Value{{int64(1), "protected"}, {int64(2), "protected"}}
				if got := tuples(roleExecute(t, e, admin, "SELECT a,b FROM t ORDER BY a")); !reflect.DeepEqual(got, wantRows) {
					t.Fatal(got, wantRows)
				}
				result, err = execute("INSERT INTO t VALUES(1,'protected')")
				if result.Count != 0 || result.Rows != nil {
					t.Fatal("duplicate returned output", result)
				}
				runtimeDetail := ""
				if mode == "all" || mode == "table" {
					runtimeDetail = "Key (a, b)=(1, protected) already exists."
				}
				nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "23505", Message: `duplicate key value violates unique constraint "u"`, Detail: runtimeDetail, Schema: "public", Table: "t", Constraint: "u"})
				lockEmpty(t, e)
			})
		}
	}
}
