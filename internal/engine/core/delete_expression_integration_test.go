package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestDeleteCoreExpressionPreflightAuthority(t *testing.T) {
	for _, mode := range []string{"authority", "metadata", "epoch", "preflight", "relock", "teardown", "success"} {
		t.Run(mode, func(t *testing.T) {
			e := engine.New()
			enginetest.Run(t, e, "CREATE TABLE t(i INT4)")
			enginetest.Run(t, e, "INSERT INTO t VALUES(7)")
			enginetest.Run(t, e, "CREATE ROLE actor LOGIN")
			actor, _ := e.Roles().Lookup("actor")
			op, done, err := enginetest.Operation(e, context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer done()
			ctx := &castPreflightContext{Context: op.Ctx, e: e, t: t, cancelAt: mode}
			op.Ctx = ctx
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			// Abort and release statement admission if a later assertion exits early.
			defer scope.Finish(context.Canceled)
			var restore func()
			ctx.visit = func() {
				if scope.View != nil || len(scope.Stage.Deletes) != 0 {
					t.Error("DELETE captured data or staged a row before preflight")
				}
				switch mode {
				case "authority":
					op.Session.Mu.Lock()
					op.Session.Authorization.State.CurrentUser = actor.Ref
					op.Session.Mu.Unlock()
				case "metadata":
					address := scope.LockedRef.Address
					original := e.Catalog.State.Get(address)
					replacement := *original.(*catalog.Relation)
					e.Catalog.State.SetDescriptor(address, &replacement)
					restore = func() { e.Catalog.State.SetDescriptor(address, original) }
				case "epoch":
					original := e.Catalog.State.Epoch()
					e.Catalog.State.SetEpoch(e.Catalog.State.Epoch() + 1)
					restore = func() { e.Catalog.State.SetEpoch(original) }
				case "teardown":
					op.Session.RequestEnd(session.CleanClose)
				}
			}
			result, executeErr := scope.Execute(enginetest.Statement(t, "DELETE FROM t WHERE int4($1::text)=i"), 1, func(c context.Context, signature analyze.ParameterInfo, frame []sqltype.Value, environment sqltype.InputEnvironment) error {
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
				e.Mu.Lock()
				restore()
				e.Mu.Unlock()
			}
			if !ctx.entered {
				t.Error("DELETE preflight was not exercised")
			}
			switch mode {
			case "authority":
				enginetest.State(t, executeErr, "42501")
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
			if executeErr != nil && (result.Count != 0 || result.Rows != nil || scope.View != nil) {
				t.Error("failed preflight acquired a view or exposed a result")
			}
			if err := scope.Finish(executeErr); err != executeErr {
				t.Error(err, executeErr)
			}
			if scope.View != nil || scope.LockTicket != nil || scope.LockedStorage != nil || !scope.Finished {
				t.Error("DELETE resources survived Finish")
			}
		})
	}
}

func TestDeleteCoreExpressionPreflightEmptyAndDecisive(t *testing.T) {
	for _, predicate := range []string{"int4($1::text)=i", "FALSE AND int4($1::text)=i", "TRUE OR int4($1::text)=i"} {
		for _, populated := range []bool{false, true} {
			t.Run(predicate+map[bool]string{false: "/empty", true: "/populated"}[populated], func(t *testing.T) {
				e := engine.New()
				enginetest.Run(t, e, "CREATE TABLE t(i INT4)")
				if populated {
					enginetest.Run(t, e, "INSERT INTO t VALUES(7)")
				}
				op, done, err := enginetest.Operation(e, context.Background())
				if err != nil {
					t.Fatal(err)
				}
				defer done()
				scope, err := e.StartStatement(op)
				if err != nil {
					t.Fatal(err)
				}
				result, executeErr := scope.Execute(enginetest.Statement(t, "DELETE FROM t WHERE "+predicate), 1, func(c context.Context, signature analyze.ParameterInfo, frame []sqltype.Value, environment sqltype.InputEnvironment) error {
					value, err := signature.Types[0].Type.Input(c, "bad", -1)
					frame[0] = value
					return err
				})
				enginetest.State(t, executeErr, "22P02")
				if result.Count != 0 || result.Rows != nil || scope.View != nil || len(scope.Stage.Deletes) != 0 {
					t.Error("frame conversion failure captured or mutated rows")
				}
				if err := scope.Finish(executeErr); err != executeErr {
					t.Error(err, executeErr)
				}
				want := 0
				if populated {
					want = 1
				}
				survivors := enginetest.Run(t, e, "SELECT i FROM t")
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
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, "CREATE USER expression_actor")
	enginetest.RoleExecute(t, e, op, "CREATE TABLE t(i INT,s TEXT)")
	enginetest.RoleExecute(t, e, op, "INSERT INTO t VALUES(1,'oops')")
	enginetest.RoleExecute(t, e, op, "CREATE SCHEMA hidden")
	enginetest.RoleExecute(t, e, op, "CREATE TABLE hidden.t(i INT)")
	enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION expression_actor")
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
			result, executeErr := scope.Execute(enginetest.Statement(t, tc.query), count, func(c context.Context, signature analyze.ParameterInfo, frame []sqltype.Value, environment sqltype.InputEnvironment) error {
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
			if result.Count != 0 || result.Rows != nil || scope.View != nil || len(scope.Stage.Deletes) != 0 {
				t.Error("failed precedence captured rows")
			}
			if err := scope.Finish(executeErr); err != executeErr {
				t.Error(err)
			}
		})
	}
	enginetest.RoleExecute(t, e, op, "RESET SESSION AUTHORIZATION")
	result := enginetest.RoleExecute(t, e, op, "SELECT i FROM t")
	if result.Count != 1 {
		t.Error("ACL failure mutated rows", result.Count)
	}
	if result.Rows != nil {
		result.Rows.Close()
	}
}

func TestDeleteCoreRowCastLateAtomicity(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE TABLE t(i INT,s TEXT)")
	enginetest.Run(t, e, "INSERT INTO t VALUES(1,'42'),(2,'oops')")
	op, done, err := enginetest.Operation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	result, executeErr := scope.Execute(enginetest.Statement(t, "DELETE FROM t WHERE int4(s) IS DISTINCT FROM 0"), 0, nil)
	enginetest.State(t, executeErr, "22P02")
	root := scope.Stage.Deletes[scope.LockedRef.Address]
	if result.Count != 0 || result.Rows != nil || root == nil || len(root.Rows) != 1 {
		t.Fatal("actual first row was not staged before late cast failure", result, root)
	}
	if err := scope.Finish(executeErr); err != executeErr {
		t.Fatal(err)
	}
	rows := enginetest.Run(t, e, "SELECT i FROM t")
	if rows.Count != 2 {
		t.Fatal("late failure published partial deletion", rows.Count)
	}
	if rows.Rows != nil {
		rows.Rows.Close()
	}
	enginetest.LockEmpty(t, e)
}

// The importer arms this context after analysis/import. The first following
// cancellation checkpoint is preflight entry. TryLock observes lock ownership
// deterministically, without sleeps, goroutines, or a production test hook.
type castPreflightContext struct {
	context.Context
	e        *core.Engine
	t        *testing.T
	armed    bool
	entered  bool
	cancelAt string
	visit    func()
}

func (c *castPreflightContext) Err() error {
	if !c.armed {
		return c.Context.Err()
	}
	if !c.e.Mu.TryLock() {
		if !c.entered {
			c.t.Error("SELECT preflight entered with Engine.mu held")
			return context.Canceled
		}
		if c.cancelAt == "relock" {
			return context.Canceled
		}
		return c.Context.Err()
	}
	defer c.e.Mu.Unlock()
	if !c.entered {
		c.entered = true
		c.visit()
	}
	if c.cancelAt == "preflight" {
		return context.Canceled
	}
	return c.Context.Err()
}
