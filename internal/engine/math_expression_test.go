package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestMathBinding(t *testing.T) {
	e := New()
	b := newExpressionBinder(context.Background(), e, sqltype.InputEnvironment{}, nil, nil)
	bind := func(query string) BoundExpr {
		t.Helper()
		raw := statement(t, query).(*parser.Select).Targets[0]
		node, err := b.bind(raw, expressionPolicy{context: projectionContext})
		if err != nil {
			t.Fatal(query, err)
		}
		return node
	}
	first := bind("SELECT round(1.25,1)")
	if !b.sameGroupExpression(first, bind("SELECT round(1.25,1)")) {
		t.Fatal("equivalent selected math grouping expression lost structural identity")
	}
	if b.sameGroupExpression(first, bind("SELECT trunc(1.25,1)")) || b.sameGroupExpression(first, bind("SELECT round(1.25,2)")) {
		t.Fatal("different math operation/scale shared grouping identity")
	}
	for _, query := range []string{"SELECT trunc(NULL)", "SELECT trunc('-0.5')", "SELECT trunc($1)"} {
		raw := statement(t, query).(*parser.Select).Targets[0]
		_, err := b.bind(raw, expressionPolicy{context: projectionContext})
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42725" || diagnostic.Message != "function trunc(unknown) is not unique" {
			t.Fatal("released unknown trunc category ambiguity", query, err)
		}
	}
	op, done := roleOperation(t, e, "postgres")
	defer done()
	roleExecute(t, e, op, "CREATE USER math_reader")
	roleExecute(t, e, op, "CREATE TABLE m(n NUMERIC,unused NUMERIC)")
	roleExecute(t, e, op, "INSERT INTO m VALUES(1.25,9)")
	reader, _ := e.Roles().Lookup("math_reader")
	grantor := e.roles.names["postgres"]
	ref := roleTableRef(t, e, "m")
	grant := func(subID int32) {
		t.Helper()
		column := ref
		column.Address.SubID = subID
		if err := e.SetObjectACL(context.Background(), column, ACLValue{Explicit: true, Entries: []ACLEntry{{Grantor: grantor, Grantee: ACLGrantee{Role: reader.Ref}, Privileges: SelectPrivilege}}}); err != nil {
			t.Fatal(err)
		}
	}
	grant(2)
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION math_reader")
	for _, query := range []string{"SELECT round(trim_scale(n),1) FROM m", "SELECT round(n,1),count(*) FROM m GROUP BY 1"} {
		ast := statement(t, query)
		if _, err := e.Describe(op, ast); err != nil {
			t.Fatal("description", err)
		}
		_, err := e.Execute(op, ast, 0, nil)
		var diagnostic *sqlstate.Error
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for table m"}
		if !errors.As(err, &diagnostic) || *diagnostic != want {
			t.Fatal("hidden math column authorization", diagnostic, err)
		}
	}
	roleExecute(t, e, op, "RESET SESSION AUTHORIZATION")
	grant(1)
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION math_reader")
	result, err := e.Execute(op, statement(t, "SELECT round(trim_scale(n),1) FROM m"), 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := mathTuples(t, result)
	if len(got) != 1 || got[0][0] != "1.3" {
		t.Fatal(got)
	}
}
