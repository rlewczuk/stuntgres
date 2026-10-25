package core_test

import (
	"context"
	"database/sql/driver"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestUpdateCoreColumnAuthority(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, "CREATE ROLE update_reader")
	enginetest.RoleExecute(t, e, op, "CREATE TABLE t(a INT,b INT,c INT)")
	enginetest.RoleExecute(t, e, op, "INSERT INTO t VALUES(1,2,3)")
	ref := enginetest.RoleTableRef(t, e, "t")
	role, _ := e.Roles().Lookup("update_reader")
	column := ref
	column.Address.SubID = 1
	grant := func(ref ids.ObjectRef, privileges authorization.Privileges) {
		t.Helper()
		err := enginetest.SetObjectACL(e, context.Background(), ref, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), Grantee: authorization.ACLGrantee{Role: role.Ref}, Privileges: privileges}}})
		if err != nil {
			t.Fatal(err)
		}
	}
	grant(column, authorization.UpdatePrivilege)
	enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION update_reader")
	// Carrying protected b/c forward does not require SELECT on them.
	enginetest.RoleExecute(t, e, op, "UPDATE t SET a=7")
	for _, q := range []string{"UPDATE t SET b=8", "UPDATE t SET a=b", "UPDATE t SET a=8 WHERE c=3", "UPDATE t SET a=a"} {
		enginetest.RoleError(t, e, op, q, "42501", "permission denied for table t", "")
	}
	grant(column, authorization.UpdatePrivilege|authorization.SelectPrivilege)
	column.Address.SubID = 2
	grant(column, authorization.SelectPrivilege)
	enginetest.RoleExecute(t, e, op, "UPDATE t SET a=b WHERE a=7")
	enginetest.RoleExecute(t, e, op, "RESET SESSION AUTHORIZATION")
	result := enginetest.RoleExecute(t, e, op, "SELECT a,b,c FROM t")
	if got := enginetest.Tuples(result); !reflect.DeepEqual(got, [][]driver.Value{{int64(2), int64(2), int64(3)}}) {
		t.Fatal(got)
	}
}

func TestUpdateCoreNotNullDetailColumnAuthority(t *testing.T) {
	for _, mode := range []string{"update_only", "select_readable", "all_column_select", "table_select", "modified_readable", "owner", "superuser"} {
		for _, prepared := range []bool{false, true} {
			for _, explicit := range []bool{false, true} {
				t.Run(mode+map[bool]string{false: "/direct", true: "/prepared"}[prepared]+map[bool]string{false: "/implicit", true: "/explicit"}[explicit], func(t *testing.T) {
					e := engine.New()
					admin, adminDone := enginetest.RoleOperation(t, e, "postgres")
					defer adminDone()
					enginetest.RoleExecute(t, e, admin, "CREATE ROLE nn_acl_owner LOGIN")
					enginetest.RoleExecute(t, e, admin, "CREATE ROLE nn_acl_actor LOGIN")
					enginetest.RoleExecute(t, e, admin, "SET SESSION AUTHORIZATION nn_acl_owner")
					withPublicSchemaUnitOwner(t, e, "nn_acl_owner", func() {
						enginetest.RoleExecute(t, e, admin, "CREATE TABLE t(n INT NOT NULL,secret TEXT,readable TEXT)")
					})
					enginetest.RoleExecute(t, e, admin, "INSERT INTO t VALUES(1,'private','visible')")
					enginetest.RoleExecute(t, e, admin, "RESET SESSION AUTHORIZATION")
					ref := enginetest.RoleTableRef(t, e, "t")
					role, _ := e.Roles().Lookup("nn_acl_actor")
					grant := func(subID int32, privileges authorization.Privileges) {
						t.Helper()
						target := ref
						target.Address.SubID = subID
						acl := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("nn_acl_owner"), Grantee: authorization.ACLGrantee{Role: role.Ref}, Privileges: privileges}}}
						if err := enginetest.SetObjectACL(e, context.Background(), target, acl); err != nil {
							t.Fatal(err)
						}
					}
					grant(1, authorization.UpdatePrivilege)
					detail := "Failing row contains (n) = (null)."
					switch mode {
					case "select_readable":
						grant(3, authorization.SelectPrivilege)
						detail = "Failing row contains (n, readable) = (null, visible)."
					case "all_column_select":
						grant(1, authorization.UpdatePrivilege|authorization.SelectPrivilege)
						grant(2, authorization.SelectPrivilege)
						grant(3, authorization.SelectPrivilege)
						detail = "Failing row contains (n, secret, readable) = (null, private, visible)."
					case "table_select":
						grant(0, authorization.SelectPrivilege)
						detail = "Failing row contains (null, private, visible)."
					case "modified_readable":
						grant(3, authorization.UpdatePrivilege)
						detail = "Failing row contains (n, readable) = (null, new)."
					case "owner", "superuser":
						detail = "Failing row contains (null, private, visible)."
					}
					actor := "nn_acl_actor"
					if mode == "owner" {
						actor = "nn_acl_owner"
					}
					if mode == "superuser" {
						actor = "postgres"
					}
					op, done := enginetest.RoleOperation(t, e, actor)
					defer done()
					query := "UPDATE t SET n=NULL"
					if prepared {
						query = "UPDATE t SET n=$1"
					}
					if mode == "modified_readable" {
						if prepared {
							query = "UPDATE t SET readable='new',n=$1"
						} else {
							query = "UPDATE t SET readable='new',n=NULL"
						}
					}
					ast := enginetest.Statement(t, query)
					var expected *core.StatementDescription
					if prepared {
						description, err := e.Describe(op, ast)
						if err != nil || len(description.Parameters.Types) != 1 || description.Parameters.Types[0].Type.ID() != sqltype.Int4 {
							t.Fatal("retained parameter description", description, err)
						}
						expected = &description
					}
					original := append([]storage.RowVersion(nil), e.Storage.Relations[ref.Address].Versions...)
					repeats := 1
					if prepared {
						repeats = 2
					}
					for range repeats {
						if explicit {
							enginetest.RoleExecute(t, e, op, "BEGIN")
							enginetest.RoleExecute(t, e, op, "UPDATE t SET n=2")
						}
						scope, err := e.StartStatement(op)
						if err != nil {
							t.Fatal(err)
						}
						scope.Expected = expected
						var result core.Result
						var executionErr error
						if prepared {
							result, executionErr = scope.Execute(ast, 1, func(ctx context.Context, signature analyze.ParameterInfo, frame []sqltype.Value, environment sqltype.InputEnvironment) error {
								frame[0] = sqltype.Null(signature.Types[0].Type)
								return nil
							})
						} else {
							result, executionErr = scope.Execute(ast, 0, nil)
						}
						finishErr := scope.Finish(executionErr)
						if finishErr != executionErr {
							t.Fatal("Finish changed constraint cause", finishErr, executionErr)
						}
						if result.Count != 0 || result.Rows != nil {
							t.Fatal("constraint exposed result", result)
						}
						nullCountUnitDiagnostic(t, executionErr, sqlstate.Error{Code: "23502", Message: `null value in column "n" of relation "t" violates not-null constraint`, Detail: detail, Schema: "public", Table: "t", Column: "n"})
						if explicit {
							if op.Session.TransactionState() != session.TxAborted || len(op.Session.Transactions.Writes) != 0 {
								t.Fatal("prior private UPDATE survived failed block")
							}
							enginetest.RoleError(t, e, op, "UPDATE t SET n=3", "25P02", "current transaction is aborted, commands ignored until end of transaction block", "")
							enginetest.RoleExecute(t, e, op, "ROLLBACK")
						}
						if !reflect.DeepEqual(e.Storage.Relations[ref.Address].Versions, original) {
							t.Fatal("failed UPDATE changed protected row or version")
						}
						result = enginetest.RoleExecute(t, e, admin, "SELECT n,secret,readable FROM t")
						if got := enginetest.Tuples(result); !reflect.DeepEqual(got, [][]driver.Value{{int64(1), "private", "visible"}}) {
							t.Fatal("failed UPDATE changed original row", got)
						}
						if err := result.Rows.Close(); err != nil {
							t.Fatal(err)
						}
						enginetest.LockEmpty(t, e)
					}
				})
			}
		}
	}
}
