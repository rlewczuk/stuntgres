package engine

import (
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestQueryLockContextsAndReferenceKinds(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	roleExecute(t, e, op, "CREATE TABLE t(id INT)")
	roleExecute(t, e, op, "CREATE SEQUENCE s")
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
			raw := statement(t, tc.q).(*parser.Select)
			e.mu.Lock()
			auth, err := e.authorizationLocked(op)
			binder := newExpressionBinder(op.Context(), e, sqltype.InputEnvironment{}, op, nil)
			binder.query.auth = &auth
			var query *plan.Query
			if err == nil {
				query, err = binder.BindSelect(raw, false)
			}
			if err == nil {
				err = e.initializeQueryLocksLocked(scope, query.Root, binder.query.sources)
			}
			e.mu.Unlock()
			if tc.sequenceError {
				waitsDiagnostic(t, err, "42809", `cannot lock rows in sequence "s"`)
			} else {
				if err != nil || (scope.queryLocks != nil) != tc.marked {
					t.Fatal(err, scope.queryLocks, tc.marked)
				}
			}
			lockState := e.locks.Inspect()
			grants := len(lockState.Resources)
			if grants != 0 {
				t.Fatal("kind/context preflight acquired physical resources", grants)
			}
		})
	}
}
