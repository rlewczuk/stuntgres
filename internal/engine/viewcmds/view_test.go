package viewcmds_test

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/sqlstate"
)

func TestViewOriginalInvokerAuthority(t *testing.T) {
	e := engine.New()
	admin, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{"CREATE USER view_reader", "CREATE TABLE t(id INT)", "INSERT INTO t VALUES(7)", "CREATE VIEW inner_view WITH(security_invoker=true) AS SELECT id FROM t", "CREATE VIEW outer_view AS SELECT id FROM inner_view"} {
		enginetest.RoleExecute(t, e, admin, q)
	}
	reader, _ := e.Roles().Lookup("view_reader")
	grantor := e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
	grant := func(name string, rights authorization.Privileges) {
		t.Helper()
		ref := enginetest.RoleTableRef(t, e, name)
		if err := enginetest.SetObjectACL(e, context.Background(), ref, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: grantor, Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: rights}}}); err != nil {
			t.Fatal(err)
		}
	}
	grant("outer_view", authorization.SelectPrivilege)
	grant("t", 0)
	enginetest.RoleExecute(t, e, admin, "SET SESSION AUTHORIZATION view_reader")
	ast := enginetest.Statement(t, "SELECT id FROM outer_view")
	execute := func(denied bool) {
		t.Helper()
		scope, err := e.StartStatement(admin)
		if err != nil {
			t.Fatal(err)
		}
		result, err := scope.Execute(ast, 0, nil)
		err = scope.Finish(err)
		if denied {
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != "42501" || diagnostic.Message != "permission denied for table t" || result.Rows != nil {
				t.Fatal(result, err)
			}
		} else {
			if err != nil || result.Count != 1 || result.Rows == nil {
				t.Fatal(result, err)
			}
			result.Rows.Close()
		}
	}
	execute(true)
	grant("t", authorization.SelectPrivilege)
	execute(false)
	grant("t", 0)
	execute(true)
}

func TestViewPreparedOriginalInvokerAuthority(t *testing.T) {
	e := engine.New()
	admin, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	for _, query := range []string{"CREATE USER prepared_view_reader", "CREATE TABLE t(id INT)", "INSERT INTO t VALUES(7)", "CREATE VIEW invoker_view WITH(security_invoker=true) AS SELECT id FROM t", "CREATE VIEW owner_view AS SELECT id FROM invoker_view"} {
		enginetest.RoleExecute(t, e, admin, query)
	}
	reader, _ := e.Roles().Lookup("prepared_view_reader")
	grantor := e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
	grant := func(name string, rights authorization.Privileges) {
		t.Helper()
		if err := enginetest.SetObjectACL(e, context.Background(), enginetest.RoleTableRef(t, e, name), authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: grantor, Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: rights}}}); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"invoker_view", "owner_view", "t"} {
		grant(name, authorization.SelectPrivilege)
	}
	enginetest.RoleExecute(t, e, admin, "SET SESSION AUTHORIZATION prepared_view_reader")
	for _, name := range []string{"invoker_view", "owner_view"} {
		ast := enginetest.Statement(t, "SELECT id FROM "+name)
		description, err := e.Describe(admin, ast)
		if err != nil {
			t.Fatal(err)
		}
		if len(description.Results) != 1 || description.Results[0].Name != "id" || description.Results[0].Usage.Type.DatabaseName() != "INT4" {
			t.Fatal("prepared metadata", description.Results)
		}
		savedResult := description.Results[0]
		execute := func(prepared, denied bool) {
			t.Helper()
			before := e.Catalog.State
			scope, err := e.StartStatement(admin)
			if err != nil {
				t.Fatal(err)
			}
			if prepared {
				scope.Expected = &description
			}
			result, err := scope.Execute(ast, 0, nil)
			err = scope.Finish(err)
			if denied {
				var diagnostic *sqlstate.Error
				want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for table t"}
				if !errors.As(err, &diagnostic) || *diagnostic != want || result.Rows != nil || result.Count != 0 || e.Catalog.State != before {
					t.Fatal(name, prepared, result, err)
				}
				return
			}
			if err != nil || result.Rows == nil {
				t.Fatal(name, prepared, result, err)
			}
			defer result.Rows.Close()
			schema := result.Rows.Schema()
			if len(schema) != 1 || schema[0] != savedResult || description.Results[0] != savedResult {
				t.Fatal("retained metadata changed", schema, description.Results)
			}
			rows := enginetest.Tuples(result)
			if len(rows) != 1 || len(rows[0]) != 1 || rows[0][0] != int64(7) {
				t.Fatal(rows)
			}
		}
		execute(true, false)
		grant("t", 0)
		execute(false, true)
		execute(true, true)
		grant("t", authorization.SelectPrivilege)
		execute(false, false)
		execute(true, false)
	}
}

func TestViewCreateDoesNotExecuteDefinition(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE SEQUENCE s")
	enginetest.Run(t, e, "CREATE TABLE t(id INT)")
	enginetest.Run(t, e, "INSERT INTO t VALUES(1)")
	enginetest.Run(t, e, "CREATE VIEW v AS SELECT nextval('s') FROM t")
	enginetest.Run(t, e, "CREATE TABLE u(id INT)")
	enginetest.Run(t, e, "INSERT INTO u VALUES(1)")
	enginetest.Run(t, e, "CREATE VIEW joined AS SELECT nextval('s') FROM t JOIN u ON t.id=u.id")
	result := enginetest.Run(t, e, "SELECT last_value,is_called FROM s")
	rows := enginetest.Tuples(result)
	if len(rows) != 1 || rows[0][0] != int64(1) || rows[0][1] != false {
		t.Fatal(rows)
	}
}

func TestViewFrozenRelativeLiteralNativeReplay(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE TABLE t(id INT)")
	enginetest.Run(t, e, "INSERT INTO t VALUES(1)")
	op, done, err := enginetest.Operation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	create := enginetest.Statement(t, "CREATE VIEW frozen(d) AS SELECT DATE 'today' FROM t")
	// Establish the implicit transaction before fixing its authoritative clock.
	// Execute refreshes scope.input from this owner; setting scope.input directly
	// would be overwritten.
	if err = scope.Gate(create); err != nil {
		_ = scope.Finish(err)
		t.Fatal(err)
	}
	scope.Owner.Mu.Lock()
	scope.Owner.Start = time.Date(2003, 7, 7, 4, 5, 6, 0, time.UTC)
	scope.Owner.Mu.Unlock()
	_, err = scope.Execute(create, 0, nil)
	if err = scope.Finish(err); err != nil {
		t.Fatal(err)
	}
	check := func(phase string) {
		t.Helper()
		result := enginetest.Run(t, e, "SELECT d=DATE '2003-07-07' FROM frozen")
		defer result.Rows.Close()
		rows := enginetest.Tuples(result)
		if len(rows) != 1 || len(rows[0]) != 1 || rows[0][0] != true {
			t.Fatal(phase, "relative literal was reinterpreted", rows)
		}
	}
	check("before Dump")
	var dump bytes.Buffer
	if err := engine.Dump(context.Background(), e, &dump); err != nil {
		t.Fatal(err)
	}
	if err := engine.Restore(context.Background(), e, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	check("after Restore")
}
