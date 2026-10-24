package engine

import (
	"bytes"
	"context"
	"errors"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/sqlstate"
	"testing"
	"time"
)

func TestViewOriginalInvokerAuthority(t *testing.T) {
	e := New()
	admin, done := roleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{"CREATE USER view_reader", "CREATE TABLE t(id INT)", "INSERT INTO t VALUES(7)", "CREATE VIEW inner_view WITH(security_invoker=true) AS SELECT id FROM t", "CREATE VIEW outer_view AS SELECT id FROM inner_view"} {
		roleExecute(t, e, admin, q)
	}
	reader, _ := e.Roles().Lookup("view_reader")
	grantor := e.roles.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
	grant := func(name string, rights authorization.Privileges) {
		t.Helper()
		ref := roleTableRef(t, e, name)
		if err := e.SetObjectACL(context.Background(), ref, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: grantor, Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: rights}}}); err != nil {
			t.Fatal(err)
		}
	}
	grant("outer_view", authorization.SelectPrivilege)
	grant("t", 0)
	roleExecute(t, e, admin, "SET SESSION AUTHORIZATION view_reader")
	ast := statement(t, "SELECT id FROM outer_view")
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
	e := New()
	admin, done := roleOperation(t, e, "postgres")
	defer done()
	for _, query := range []string{"CREATE USER prepared_view_reader", "CREATE TABLE t(id INT)", "INSERT INTO t VALUES(7)", "CREATE VIEW invoker_view WITH(security_invoker=true) AS SELECT id FROM t", "CREATE VIEW owner_view AS SELECT id FROM invoker_view"} {
		roleExecute(t, e, admin, query)
	}
	reader, _ := e.Roles().Lookup("prepared_view_reader")
	grantor := e.roles.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
	grant := func(name string, rights authorization.Privileges) {
		t.Helper()
		if err := e.SetObjectACL(context.Background(), roleTableRef(t, e, name), authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: grantor, Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: rights}}}); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"invoker_view", "owner_view", "t"} {
		grant(name, authorization.SelectPrivilege)
	}
	roleExecute(t, e, admin, "SET SESSION AUTHORIZATION prepared_view_reader")
	for _, name := range []string{"invoker_view", "owner_view"} {
		ast := statement(t, "SELECT id FROM "+name)
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
			before := e.catalog.State
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
				if !errors.As(err, &diagnostic) || *diagnostic != want || result.Rows != nil || result.Count != 0 || e.catalog.State != before {
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
			rows := tuples(result)
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
	e := New()
	run(t, e, "CREATE SEQUENCE s")
	run(t, e, "CREATE TABLE t(id INT)")
	run(t, e, "INSERT INTO t VALUES(1)")
	run(t, e, "CREATE VIEW v AS SELECT nextval('s') FROM t")
	run(t, e, "CREATE TABLE u(id INT)")
	run(t, e, "INSERT INTO u VALUES(1)")
	run(t, e, "CREATE VIEW joined AS SELECT nextval('s') FROM t JOIN u ON t.id=u.id")
	result := run(t, e, "SELECT last_value,is_called FROM s")
	rows := tuples(result)
	if len(rows) != 1 || rows[0][0] != int64(1) || rows[0][1] != false {
		t.Fatal(rows)
	}
}

func TestViewFrozenRelativeLiteralNativeReplay(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE t(id INT)")
	run(t, e, "INSERT INTO t VALUES(1)")
	op, done, err := testOperation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	create := statement(t, "CREATE VIEW frozen(d) AS SELECT DATE 'today' FROM t")
	// Establish the implicit transaction before fixing its authoritative clock.
	// Execute refreshes scope.input from this owner; setting scope.input directly
	// would be overwritten.
	if err = scope.gate(create); err != nil {
		_ = scope.Finish(err)
		t.Fatal(err)
	}
	scope.owner.Mu.Lock()
	scope.owner.Start = time.Date(2003, 7, 7, 4, 5, 6, 0, time.UTC)
	scope.owner.Mu.Unlock()
	_, err = scope.Execute(create, 0, nil)
	if err = scope.Finish(err); err != nil {
		t.Fatal(err)
	}
	check := func(phase string) {
		t.Helper()
		result := run(t, e, "SELECT d=DATE '2003-07-07' FROM frozen")
		defer result.Rows.Close()
		rows := tuples(result)
		if len(rows) != 1 || len(rows[0]) != 1 || rows[0][0] != true {
			t.Fatal(phase, "relative literal was reinterpreted", rows)
		}
	}
	check("before Dump")
	var dump bytes.Buffer
	if err := e.Dump(context.Background(), &dump); err != nil {
		t.Fatal(err)
	}
	if err := e.Restore(context.Background(), bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	check("after Restore")
}
