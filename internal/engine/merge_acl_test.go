package engine

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestMergeInsertHeldACL(t *testing.T) {
	e := New()
	admin, adminDone := roleOperation(t, e, "postgres")
	defer adminDone()
	for _, q := range []string{"CREATE USER merge_held_writer", "CREATE TABLE t(id INT,v INT DEFAULT 9)", "CREATE TABLE source(id INT)", "INSERT INTO source VALUES(1)"} {
		roleExecute(t, e, admin, q)
	}
	owner := admin.RoleState().CurrentUser
	actor, _ := e.Roles().Lookup("merge_held_writer")
	grant := func(table string, attribute int, mask authorization.Privileges) {
		ref := roleTableRef(t, e, table)
		ref.Address.SubID = int32(attribute)
		if err := e.SetObjectACL(context.Background(), ref, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner, Grantee: authorization.ACLGrantee{Role: actor.Ref}, Privileges: mask}}}); err != nil {
			t.Fatal(err)
		}
	}
	grant("t", 1, authorization.InsertPrivilege|authorization.SelectPrivilege)
	grant("t", 2, authorization.InsertPrivilege)
	grant("source", 1, authorization.SelectPrivilege)
	writer, writerDone := roleOperation(t, e, "merge_held_writer")
	defer writerDone()
	ast := statement(t, "MERGE INTO t USING source s ON t.id=s.id WHEN NOT MATCHED AND s.id>0 THEN INSERT(id) VALUES(s.id) WHEN NOT MATCHED THEN INSERT(v) VALUES(1)")
	description, err := e.Describe(writer, ast)
	if err != nil {
		t.Fatal(err)
	}
	// Exactly the adapter's retained AST/description path; never re-Describe.
	execute := func(want int64, denied string) {
		scope, err := e.StartStatement(writer)
		if err != nil {
			t.Fatal(err)
		}
		scope.Expected = &description
		result, err := scope.Execute(ast, description.Parameters.Count, nil)
		err = scope.Finish(err)
		err = writer.CommandError(err)
		if denied == "" {
			if err != nil || result.Count != want || result.Rows != nil {
				t.Fatal(result, err)
			}
			return
		}
		var diagnostic *sqlstate.Error
		expected := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for table " + denied}
		if !errors.As(err, &diagnostic) || !reflect.DeepEqual(*diagnostic, expected) || result.Count != 0 || result.Rows != nil {
			t.Fatal("held ACL failure", result, err)
		}
	}
	execute(1, "")
	roleExecute(t, e, admin, "INSERT INTO source VALUES(2)")
	grant("t", 2, 0)
	execute(0, "t")
	result := roleExecute(t, e, admin, "SELECT id FROM t ORDER BY id")
	rows := tuples(result)
	result.Rows.Close()
	if len(rows) != 1 || rows[0][0] != int64(1) {
		t.Fatal("target revoke changed rows", rows)
	}
	grant("t", 2, authorization.InsertPrivilege)
	execute(1, "")
	roleExecute(t, e, admin, "INSERT INTO source VALUES(3)")
	grant("source", 1, 0)
	execute(0, "source")
	result = roleExecute(t, e, admin, "SELECT id FROM t ORDER BY id")
	rows = tuples(result)
	result.Rows.Close()
	if len(rows) != 2 || rows[0][0] != int64(1) || rows[1][0] != int64(2) {
		t.Fatal("source revoke changed rows", rows)
	}
	grant("source", 1, authorization.SelectPrivilege)
	execute(1, "")
	result = roleExecute(t, e, admin, "SELECT id,v FROM t ORDER BY id")
	rows = tuples(result)
	result.Rows.Close()
	if len(rows) != 3 {
		t.Fatal(rows)
	}
	for i, row := range rows {
		if row[0] != int64(i+1) || row[1] != int64(9) {
			t.Fatal(rows)
		}
	}
	lockEmpty(t, e)
}
