package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func executeUsingDescription(e *core.Engine, op *session.Operation, ast parser.Statement, description *core.StatementDescription) (core.Result, error) {
	scope, err := e.StartStatement(op)
	if err != nil {
		return core.Result{}, err
	}
	defer func() {
		if !scope.Finished {
			scope.Finish(errors.New("abandoned described DELETE USING fixture"))
		}
	}()
	scope.Expected = description
	result, err := scope.Execute(ast, 0, nil)
	err = scope.Finish(err)
	return result, err
}

func TestDeleteUsingTargetSourceColumnAuthority(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{"CREATE USER using_writer", "CREATE TABLE t(id INT,secret TEXT)", "CREATE TABLE s(k INT,secret TEXT)", "CREATE TABLE r(k INT)", "INSERT INTO t VALUES(1,'target'),(2,'kept')", "INSERT INTO s VALUES(1,'source')", "INSERT INTO r VALUES(1)"} {
		enginetest.RoleExecute(t, e, op, q)
	}
	owner := op.RoleState().CurrentUser
	actor, _ := e.Roles().Lookup("using_writer")
	grant := func(table string, attribute int, mask authorization.Privileges) {
		ref := enginetest.RoleTableRef(t, e, table)
		ref.Address.SubID = int32(attribute)
		if err := enginetest.SetObjectACL(e, context.Background(), ref, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner, Grantee: authorization.ACLGrantee{Role: actor.Ref}, Privileges: mask}}}); err != nil {
			t.Fatal(err)
		}
	}
	grant("t", 0, authorization.DeletePrivilege)
	grant("s", 1, authorization.SelectPrivilege)
	grant("r", 1, authorization.SelectPrivilege)
	enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION using_writer")
	for _, q := range []string{"DELETE FROM t USING s WHERE FALSE", "DELETE FROM t USING s JOIN r ON s.k=r.k WHERE FALSE RETURNING s.k"} {
		if r := enginetest.RoleExecute(t, e, op, q); r.Count != 0 {
			t.Fatal(r)
		} else if r.Rows != nil {
			r.Rows.Close()
		}
	}
	for _, q := range []string{"DELETE FROM t USING s WHERE t.id=s.k", "DELETE FROM t USING s WHERE FALSE RETURNING old.id", "DELETE FROM t USING s WHERE FALSE RETURNING new.id"} {
		enginetest.RoleError(t, e, op, q, "42501", "permission denied for table t", "")
	}
	grant("t", 1, authorization.SelectPrivilege)
	for _, q := range []string{"DELETE FROM t USING s WHERE t.id=s.k RETURNING s.secret", "DELETE FROM t USING s JOIN r ON s.secret='source' WHERE FALSE"} {
		enginetest.RoleError(t, e, op, q, "42501", "permission denied for table s", "")
	}
	grant("r", 1, 0)
	enginetest.RoleError(t, e, op, "DELETE FROM t USING s CROSS JOIN r WHERE FALSE RETURNING 1", "42501", "permission denied for table r", "")
	grant("r", 1, authorization.SelectPrivilege)
	ast := enginetest.Statement(t, "DELETE FROM t USING s JOIN r USING(k) WHERE t.id=s.k RETURNING old.id,new.id,s.k")
	description, err := e.Describe(op, ast)
	if err != nil {
		t.Fatal(err)
	}
	r, err := executeUsingDescription(e, op, ast, &description)
	if r.Rows != nil {
		defer r.Rows.Close()
	}
	if err != nil || r.Count != 1 {
		t.Fatal(r, err)
	}
	r.Rows.Close()
	enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION postgres")
	enginetest.RoleExecute(t, e, op, "INSERT INTO t VALUES(1,'restored')")
	enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION using_writer")
	for _, target := range []string{"s", "t"} {
		if target == "s" {
			grant("s", 1, 0)
		} else {
			grant("s", 1, authorization.SelectPrivilege)
			grant("t", 0, 0)
		}
		r, err = executeUsingDescription(e, op, ast, &description)
		var diagnostic *sqlstate.Error
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for table " + target}
		if r.Count != 0 || r.Rows != nil || !errors.As(err, &diagnostic) || *diagnostic != want {
			t.Fatal(r, err, diagnostic, want)
		}
	}
	enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION postgres")
	result := enginetest.RoleExecute(t, e, op, "SELECT id FROM t ORDER BY id")
	if rows := enginetest.Tuples(result); len(rows) != 2 || rows[0][0] != int64(1) || rows[1][0] != int64(2) {
		t.Fatal(rows)
	}
	result.Rows.Close()
	enginetest.LockEmpty(t, e)
}
