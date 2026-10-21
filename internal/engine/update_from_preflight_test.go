package engine

import (
	"context"
	"database/sql/driver"
	"reflect"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestUpdateFromImportedPreflightCopiesOnlyChangedTree(t *testing.T) {
	e := New()
	session := sessionStart(t, e, nil)
	for _, q := range []string{"CREATE TABLE t(i INT)", "CREATE TABLE s(k INT)", "CREATE TABLE r(k INT)"} {
		sessionSQL(t, session, q)
	}
	op, scope, b, target, from := updateFromBoundSources(t, e, session, context.Background(), "UPDATE t SET i=int4($1::text) FROM s JOIN r ON s.k=int4($1::text) WHERE t.i=int4($1::text) RETURNING int4($1::text),s.k")
	defer op.Done()
	defer scope.Finish(nil)
	defer target.Close()
	defer from.Close()
	original := b.Update
	output := b.Returning
	cast := original.Assignments[0].(plan.CoercionExpr)
	join := original.From.(*plan.JoinNode)
	predicate := original.Predicate
	for _, input := range []string{"7", "bad", "17", "bad", "42"} {
		value, err := cast.Child.ResultUsage().Type.Input(context.Background(), input, -1)
		if err != nil {
			t.Fatal(err)
		}
		eval := exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Parameters: []sqltype.Value{value}}}
		private, err := preflightUpdate(&eval, original)
		if input == "bad" {
			state(t, err, "22P02")
			if private != nil {
				t.Fatal(private)
			}
		} else {
			if err != nil || private == original || private.From == original.From || reflect.DeepEqual(private.Predicate, predicate) || &private.Assignments[0] == &original.Assignments[0] {
				t.Fatal("preflight failed copy-on-change", private, err)
			}
			foldedJoin := private.From.(*plan.JoinNode)
			if foldedJoin.Left != join.Left || foldedJoin.Right != join.Right || !reflect.DeepEqual(foldedJoin.QuerySchema, join.QuerySchema) || reflect.DeepEqual(foldedJoin.Predicate, join.Predicate) {
				t.Fatal("source preflight rebuilt unchanged providers", foldedJoin, join)
			}
			if _, ok := foldedJoin.Predicate.(plan.ComparisonExpr).Right.(plan.LiteralExpr); !ok {
				t.Fatal("ON coercion was not eagerly traversed")
			}
			folded, err := dml.PreflightReturning(&eval, output)
			if err != nil || folded == output || !reflect.DeepEqual(folded.Schema, output.Schema) || !reflect.DeepEqual(folded.Expressions[1], output.Expressions[1]) {
				t.Fatal(folded, err)
			}
			result, err := private.Assignments[0].Eval(&eval)
			if err != nil || testExport(result) != map[string]int64{"7": 7, "17": 17, "42": 42}[input] {
				t.Fatal(result, err)
			}
		}
		if _, ok := original.Assignments[0].(plan.CoercionExpr); !ok {
			t.Fatal("analyzed SET mutated")
		}
		if _, ok := join.Predicate.(plan.ComparisonExpr).Right.(plan.CoercionExpr); !ok {
			t.Fatal("analyzed ON mutated")
		}
		if _, ok := output.Expressions[0].(plan.CoercionExpr); !ok {
			t.Fatal("analyzed output mutated")
		}
	}
	unchanged := &analyze.UpdateBinding{Assignments: []expr.Expr{output.Expressions[1]}, From: join.Left}
	eval := exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}
	same, err := preflightUpdate(&eval, unchanged)
	if err != nil || same != unchanged {
		t.Fatal("unchanged tree replaced", same, err)
	}
}

func TestUpdateFromSeparatesTargetAndSourceColumnAuthority(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{"CREATE USER from_writer", "CREATE TABLE t(a INT,secret TEXT)", "CREATE TABLE s(k INT,secret TEXT)", "CREATE TABLE r(k INT)", "INSERT INTO t VALUES(10,'target')", "INSERT INTO s VALUES(7,'source')", "INSERT INTO r VALUES(7)"} {
		roleExecute(t, e, op, q)
	}
	owner := op.RoleState().CurrentUser
	actor, _ := e.Roles().Lookup("from_writer")
	grant := func(table string, attribute int, mask authorization.Privileges) {
		t.Helper()
		ref := roleTableRef(t, e, table)
		ref.Address.SubID = int32(attribute)
		if err := e.SetObjectACL(context.Background(), ref, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner, Grantee: authorization.ACLGrantee{Role: actor.Ref}, Privileges: mask}}}); err != nil {
			t.Fatal(err)
		}
	}
	grant("t", 1, authorization.UpdatePrivilege)
	grant("s", 1, authorization.SelectPrivilege)
	grant("r", 1, authorization.SelectPrivilege)
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION from_writer")
	result := roleExecute(t, e, op, "UPDATE t SET a=s.k FROM s JOIN r ON s.k=r.k RETURNING s.k")
	if result.Count != 1 {
		t.Fatal(result)
	}
	rows := tuples(result)
	result.Rows.Close()
	if !reflect.DeepEqual(rows, [][]driver.Value{{int64(7)}}) {
		t.Fatal(rows)
	}
	for _, q := range []string{"UPDATE t SET a=s.k FROM s RETURNING t.a", "UPDATE t SET a=t.a FROM s WHERE FALSE", "UPDATE t SET a=1 FROM s WHERE FALSE RETURNING old.a", "UPDATE t SET a=s.k FROM s RETURNING new.a", "UPDATE t SET a=s.k FROM s RETURNING *"} {
		roleError(t, e, op, q, "42501", "permission denied for table t", "")
	}
	for _, q := range []string{"UPDATE t SET a=1 FROM s WHERE FALSE RETURNING s.secret", "UPDATE t SET a=1 FROM s JOIN r ON s.secret='source' WHERE FALSE", "UPDATE t SET a=s.k FROM s RETURNING s.*"} {
		roleError(t, e, op, q, "42501", "permission denied for table s", "")
	}
	grant("r", 1, 0)
	roleError(t, e, op, "UPDATE t SET a=1 FROM s CROSS JOIN r WHERE FALSE RETURNING 1", "42501", "permission denied for table r", "")
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION postgres")
	result = roleExecute(t, e, op, "SELECT a,secret FROM t")
	rows = tuples(result)
	result.Rows.Close()
	if !reflect.DeepEqual(rows, [][]driver.Value{{int64(7), "target"}}) {
		t.Fatal(rows)
	}
	lockEmpty(t, e)
}
