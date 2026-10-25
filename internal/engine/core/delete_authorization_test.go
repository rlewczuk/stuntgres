package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestDeleteCoreAuthorization(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, "CREATE USER reader")
	enginetest.RoleExecute(t, e, op, "CREATE USER selector")
	enginetest.RoleExecute(t, e, op, "CREATE TABLE t(a INT,b INT)")
	enginetest.RoleExecute(t, e, op, "INSERT INTO t VALUES(1,2),(3,4)")
	ref := enginetest.RoleTableRef(t, e, "t")
	owner := op.RoleState().CurrentUser
	reader, _ := e.Roles().Lookup("reader")
	selector, _ := e.Roles().Lookup("selector")
	grant := func(mask authorization.Privileges) {
		t.Helper()
		acl := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{
			{Grantor: owner, Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: mask},
			{Grantor: owner, Grantee: authorization.ACLGrantee{Role: selector.Ref}, Privileges: authorization.SelectPrivilege},
		}}
		if err := enginetest.SetObjectACL(e, context.Background(), ref, acl); err != nil {
			t.Fatal(err)
		}
	}
	grant(authorization.DeletePrivilege)
	enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION reader")
	for _, query := range []string{"DELETE FROM t WHERE FALSE", "DELETE FROM t WHERE NULL"} {
		enginetest.RoleExecute(t, e, op, query)
	}
	ast := enginetest.Statement(t, "DELETE FROM t AS d WHERE d.a=1 AND d.a=1")
	prepared, err := e.Describe(op, ast)
	if err != nil {
		t.Fatal("ordinary DELETE ACL rejected at Prepare", err)
	}
	enginetest.RoleError(t, e, op, "DELETE FROM t WHERE a=1", "42501", "permission denied for table t", "")
	column := ref
	column.Address.SubID = 1
	if err := enginetest.SetObjectACL(e, context.Background(), column, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner, Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: authorization.DeletePrivilege}}}); err == nil {
		t.Fatal("column DELETE accepted")
	}
	if err := enginetest.SetObjectACL(e, context.Background(), column, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner, Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: authorization.SelectPrivilege}}}); err != nil {
		t.Fatal(err)
	}
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	scope.Expected = &prepared
	result, err := scope.Execute(ast, 0, nil)
	err = scope.Finish(err)
	if err != nil || result.Count != 1 || result.Rows != nil {
		t.Fatal(result, err)
	}
	enginetest.RoleError(t, e, op, "DELETE FROM t WHERE b=4", "42501", "permission denied for table t", "")
	enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION selector")
	enginetest.RoleError(t, e, op, "DELETE FROM t", "42501", "permission denied for table t", "")
	enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION reader")
	enginetest.RoleExecute(t, e, op, "DELETE FROM t") // no fabricated SELECT requirement
	grant(authorization.DeletePrivilege | authorization.SelectPrivilege)
	ast = enginetest.Statement(t, "DELETE FROM t WHERE a=1")
	prepared, err = e.Describe(op, ast)
	if err != nil {
		t.Fatal(err)
	}
	grant(authorization.SelectPrivilege) // revocation must invalidate allowance, not the handle
	scope, err = e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	scope.Expected = &prepared
	result, err = scope.Execute(ast, 0, nil)
	err = scope.Finish(err)
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for table t"}
	var diagnostic *sqlstate.Error
	if result.Count != 0 || result.Rows != nil || !errors.As(err, &diagnostic) || *diagnostic != want {
		t.Fatal(result, err, diagnostic)
	}
	view, err := engine.OperationAuthorization(op)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := view.Effective(column, false); err != nil {
		t.Fatal(err)
	}
	if err := view.Check(access.Requirement{Object: column, Privileges: authorization.DeletePrivilege}); err == nil {
		t.Fatal("column DELETE requirement accepted")
	}
	enginetest.LockEmpty(t, e)
}

func TestDeleteCoreDiagnostics(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE TABLE t(id INT)")
	enginetest.Run(t, e, "INSERT INTO t VALUES(1),(2)")
	for _, tc := range []struct{ query, code, message, position, hint string }{
		{"DELETE FROM absent", "42P01", `relation "absent" does not exist`, "13", ""},
		{"/*é*/ DELETE FROM absent", "42P01", `relation "absent" does not exist`, "19", ""},
		{"DELETE FROM t WHERE zzzzz=1", "42703", `column "zzzzz" does not exist`, "21", ""},
		{"DELETE FROM t WHERE 1", "42804", "argument of WHERE must be type boolean, not type integer", "21", ""},
		{"DELETE FROM t WHERE id=TRUE", "42883", "operator does not exist: integer = boolean", "23", "No operator matches the given name and argument types. You might need to add explicit type casts."},
		{"DELETE FROM t WHERE 'truth'", "22P02", `invalid input syntax for type boolean: "truth"`, "21", ""},
		{"DELETE FROM t AS d WHERE t.id=1", "42P01", `invalid reference to FROM-clause entry for table "t"`, "26", `Perhaps you meant to reference the table alias "d".`},
		{"DELETE FROM t AS d WHERE x.id=1", "42P01", `missing FROM-clause entry for table "x"`, "26", ""},
		{"DELETE FROM t AS d WHERE d.zzzzz=1", "42703", "column d.zzzzz does not exist", "26", ""},
		{`DELETE FROM t AS "D" WHERE "D"."Zzzzz"=1`, "42703", "column D.Zzzzz does not exist", "28", ""},
	} {
		for _, prepare := range []bool{false, true} {
			op, cleanup, err := enginetest.Operation(e, context.Background())
			if err != nil {
				t.Fatal(err)
			}
			ast := enginetest.Statement(t, tc.query)
			if prepare {
				_, err = e.Describe(op, ast)
			} else {
				_, err = e.Execute(op, ast, 0, nil)
			}
			cleanup()
			want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message, Position: tc.position, Hint: tc.hint}
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || *diagnostic != want {
				t.Fatal(tc.query, prepare, err, diagnostic, want)
			}
			got := enginetest.Run(t, e, "SELECT id FROM t ORDER BY id")
			if got.Count != 2 {
				t.Fatal("diagnostic changed rows")
			}
		}
	}
	op, cleanup, err := enginetest.Operation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	for _, query := range []string{"DELETE FROM t WHERE $1", "DELETE FROM t WHERE $1 OR $1"} {
		description, err := e.Describe(op, enginetest.Statement(t, query))
		if err != nil || description.Parameters.Count != 1 || description.Parameters.Types[0].Type.ID() != sqltype.Bool || len(description.Results) != 0 {
			t.Fatal(description, err)
		}
	}
	_, err = e.Describe(op, enginetest.Statement(t, "DELETE FROM t WHERE id=$2"))
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P18", Message: "could not determine data type of parameter $1"}
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || *diagnostic != want {
		t.Fatal(err, diagnostic)
	}
}
