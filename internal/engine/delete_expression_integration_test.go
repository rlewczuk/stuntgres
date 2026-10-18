package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestDeleteCoreExpressionPreflightAuthority(t *testing.T) {
	for _, mode := range []string{"authority", "metadata", "epoch", "preflight", "relock", "teardown", "success"} {
		t.Run(mode, func(t *testing.T) {
			e := New()
			run(t, e, "CREATE TABLE t(i INT4)")
			run(t, e, "INSERT INTO t VALUES(7)")
			run(t, e, "CREATE ROLE actor LOGIN")
			actor, _ := e.Roles().Lookup("actor")
			op, done, err := testOperation(e, context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer done()
			ctx := &castPreflightContext{Context: op.ctx, e: e, t: t, cancelAt: mode}
			op.ctx = ctx
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			// Abort and release statement admission if a later assertion exits early.
			defer scope.Finish(context.Canceled)
			var restore func()
			ctx.visit = func() {
				if scope.view != nil || len(scope.stage.deletes) != 0 {
					t.Error("DELETE captured data or staged a row before preflight")
				}
				switch mode {
				case "authority":
					op.session.mu.Lock()
					op.session.authorization.state.CurrentUser = actor.Ref
					op.session.mu.Unlock()
				case "metadata":
					address := scope.lockedRef.Address
					original := e.catalog.state.Get(address)
					replacement := *original.(*relationDescriptor)
					e.catalog.state.SetDescriptor(address, &replacement)
					restore = func() { e.catalog.state.SetDescriptor(address, original) }
				case "epoch":
					original := e.catalog.state.Epoch()
					e.catalog.state.SetEpoch(e.catalog.state.Epoch() + 1)
					restore = func() { e.catalog.state.SetEpoch(original) }
				case "teardown":
					op.session.RequestEnd(CleanClose)
				}
			}
			result, executeErr := scope.Execute(statement(t, "DELETE FROM t WHERE int4($1::text)=i"), 1, func(c context.Context, signature ParameterInfo, frame []sqltype.Value, environment sqltype.InputEnvironment) error {
				value, err := signature.Types[0].Type.Input(c, "7", -1)
				if err != nil {
					return err
				}
				frame[0] = value
				ctx.armed = true
				return nil
			})
			ctx.armed = false
			if restore != nil {
				e.mu.Lock()
				restore()
				e.mu.Unlock()
			}
			if !ctx.entered {
				t.Error("DELETE preflight was not exercised")
			}
			switch mode {
			case "authority":
				state(t, executeErr, "42501")
			case "epoch":
				if !errors.Is(executeErr, ids.ErrCatalogReplaced) {
					t.Error(executeErr)
				}
			case "preflight", "relock", "teardown":
				if !errors.Is(executeErr, context.Canceled) {
					t.Error(executeErr)
				}
			case "metadata", "success":
				if executeErr != nil || result.Count != 1 || result.Rows != nil {
					t.Error(result, executeErr)
				}
			}
			if executeErr != nil && (result.Count != 0 || result.Rows != nil || scope.view != nil) {
				t.Error("failed preflight acquired a view or exposed a result")
			}
			if err := scope.Finish(executeErr); err != executeErr {
				t.Error(err, executeErr)
			}
			if scope.view != nil || scope.ticket != nil || scope.lockedStorage != nil || !scope.finished {
				t.Error("DELETE resources survived Finish")
			}
		})
	}
}

func TestDeleteCoreExpressionPreflightEmptyAndDecisive(t *testing.T) {
	for _, predicate := range []string{"int4($1::text)=i", "FALSE AND int4($1::text)=i", "TRUE OR int4($1::text)=i"} {
		for _, populated := range []bool{false, true} {
			t.Run(predicate+map[bool]string{false: "/empty", true: "/populated"}[populated], func(t *testing.T) {
				e := New()
				run(t, e, "CREATE TABLE t(i INT4)")
				if populated {
					run(t, e, "INSERT INTO t VALUES(7)")
				}
				op, done, err := testOperation(e, context.Background())
				if err != nil {
					t.Fatal(err)
				}
				defer done()
				scope, err := e.StartStatement(op)
				if err != nil {
					t.Fatal(err)
				}
				result, executeErr := scope.Execute(statement(t, "DELETE FROM t WHERE "+predicate), 1, func(c context.Context, signature ParameterInfo, frame []sqltype.Value, environment sqltype.InputEnvironment) error {
					value, err := signature.Types[0].Type.Input(c, "bad", -1)
					frame[0] = value
					return err
				})
				state(t, executeErr, "22P02")
				if result.Count != 0 || result.Rows != nil || scope.view != nil || len(scope.stage.deletes) != 0 {
					t.Error("frame conversion failure captured or mutated rows")
				}
				if err := scope.Finish(executeErr); err != executeErr {
					t.Error(err, executeErr)
				}
				want := 0
				if populated {
					want = 1
				}
				survivors := run(t, e, "SELECT i FROM t")
				if survivors.Count != int64(want) {
					t.Error("preflight failure changed original rows", survivors.Count)
				}
				if survivors.Rows != nil {
					survivors.Rows.Close()
				}
			})
		}
	}
}

func TestDeleteCoreExpressionACLPrecedence(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	roleExecute(t, e, op, "CREATE USER expression_actor")
	roleExecute(t, e, op, "CREATE TABLE t(i INT,s TEXT)")
	roleExecute(t, e, op, "INSERT INTO t VALUES(1,'oops')")
	roleExecute(t, e, op, "CREATE SCHEMA hidden")
	roleExecute(t, e, op, "CREATE TABLE hidden.t(i INT)")
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION expression_actor")
	for _, tc := range []struct{ query, arg, code, message string }{
		{"DELETE FROM t WHERE i=int4($1::text)", "oops", "22P02", `invalid input syntax for type integer: "oops"`},
		{"DELETE FROM t WHERE i=int4($1::text)", "1", "42501", "permission denied for table t"},
		{"DELETE FROM t WHERE int4(s) IS NULL", "", "42501", "permission denied for table t"},
		{"DELETE FROM absent WHERE i=int4($1::text)", "oops", "42P01", `relation "absent" does not exist`},
		{"DELETE FROM hidden.t WHERE i=int4($1::text)", "oops", "42501", "permission denied for schema hidden"},
	} {
		t.Run(tc.query+"/"+tc.arg, func(t *testing.T) {
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			count := 0
			if tc.arg != "" {
				count = 1
			}
			result, executeErr := scope.Execute(statement(t, tc.query), count, func(c context.Context, signature ParameterInfo, frame []sqltype.Value, environment sqltype.InputEnvironment) error {
				value, err := signature.Types[0].Type.Input(c, tc.arg, -1)
				frame[0] = value
				return err
			})
			want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message}
			if tc.code == "42P01" || tc.message == "permission denied for schema hidden" {
				want.Position = "13"
			}
			var diagnostic *sqlstate.Error
			if !errors.As(executeErr, &diagnostic) || *diagnostic != want {
				t.Error(executeErr, diagnostic, want)
			}
			if result.Count != 0 || result.Rows != nil || scope.view != nil || len(scope.stage.deletes) != 0 {
				t.Error("failed precedence captured rows")
			}
			if err := scope.Finish(executeErr); err != executeErr {
				t.Error(err)
			}
		})
	}
	roleExecute(t, e, op, "RESET SESSION AUTHORIZATION")
	result := roleExecute(t, e, op, "SELECT i FROM t")
	if result.Count != 1 {
		t.Error("ACL failure mutated rows", result.Count)
	}
	if result.Rows != nil {
		result.Rows.Close()
	}
}

func TestDeleteCoreRowCastLateAtomicity(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE t(i INT,s TEXT)")
	run(t, e, "INSERT INTO t VALUES(1,'42'),(2,'oops')")
	op, done, err := testOperation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	result, executeErr := scope.Execute(statement(t, "DELETE FROM t WHERE int4(s) IS DISTINCT FROM 0"), 0, nil)
	state(t, executeErr, "22P02")
	root := scope.stage.deletes[scope.lockedRef.Address]
	if result.Count != 0 || result.Rows != nil || root == nil || len(root.rows) != 1 {
		t.Fatal("actual first row was not staged before late cast failure", result, root)
	}
	if err := scope.Finish(executeErr); err != executeErr {
		t.Fatal(err)
	}
	rows := run(t, e, "SELECT i FROM t")
	if rows.Count != 2 {
		t.Fatal("late failure published partial deletion", rows.Count)
	}
	if rows.Rows != nil {
		rows.Rows.Close()
	}
	lockEmpty(t, e)
}
