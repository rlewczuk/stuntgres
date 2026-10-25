package core_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestQueryLockContextsAndReferenceKinds(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, "CREATE TABLE t(id INT)")
	enginetest.RoleExecute(t, e, op, "CREATE SEQUENCE s")
	for _, tc := range []struct {
		q                     string
		marked, sequenceError bool
	}{
		{"SELECT id FROM t", false, false},
		{"SELECT id FROM t FOR UPDATE OF t SKIP LOCKED", true, false},
		{"SELECT x.id FROM (VALUES(1)) x(id) FOR SHARE OF x", true, false},
		{"SELECT t.id FROM t CROSS JOIN s", false, false},
		{"SELECT t.id FROM t CROSS JOIN s FOR UPDATE OF t", true, true},
	} {
		t.Run(tc.q, func(t *testing.T) {
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := scope.Finish(nil); err != nil {
					t.Error(err)
				}
			})
			raw := enginetest.Statement(t, tc.q).(*parser.Select)
			e.Mu.Lock()
			auth, err := e.AuthorizationLocked(op)
			binder := core.NewExpressionBinder(op.Context(), e, sqltype.InputEnvironment{}, op, nil)
			binder.Query.Auth = &auth
			var query *plan.Query
			if err == nil {
				query, err = binder.BindSelect(raw, false)
			}
			if err == nil {
				err = e.InitializeQueryLocksLocked(scope, query.Root, binder.Query.Sources)
			}
			e.Mu.Unlock()
			if tc.sequenceError {
				waitsDiagnostic(t, err, "42809", `cannot lock rows in sequence "s"`)
			} else {
				if err != nil || (scope.QueryLockContext != nil) != tc.marked {
					t.Fatal(err, scope.QueryLockContext, tc.marked)
				}
			}
			lockState := e.Locks.Inspect()
			grants := len(lockState.Resources)
			if grants != 0 {
				t.Fatal("kind/context preflight acquired physical resources", grants)
			}
		})
	}
}
