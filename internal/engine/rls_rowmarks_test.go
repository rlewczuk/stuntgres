package engine

import (
	"context"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestRLSPolicyRowMarks(t *testing.T) {
	e, op, done := rlsCursorFixture(t)
	defer done()
	roleExecute(t, e, op, "RESET ROLE")
	for _, sql := range []string{
		"GRANT UPDATE ON t,helper TO rls_cursor_reader",
		"ALTER POLICY p ON t USING(EXISTS(SELECT 1 FROM helper WHERE helper.id=t.id FOR SHARE))",
		"CREATE POLICY r ON t AS RESTRICTIVE USING(EXISTS(SELECT 1 FROM helper WHERE helper.id=t.id FOR SHARE))",
		"SET ROLE rls_cursor_reader",
	} {
		roleExecute(t, e, op, sql)
	}
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	finished := false
	defer func() {
		if !finished {
			_ = scope.Finish(context.Canceled)
		}
	}()
	// DECLARE's identity-only owner may already exist before a policy introduces
	// semantic marks. The ordinary initializer must promote, not replace it.
	locks := &exec.LockContext{IdentityOnly: true}
	scope.QueryLockContext = locks
	portal := &protocolPortal{}
	scope.protocol().bind = portal
	_, err = scope.Execute(statement(t, "SELECT id FROM t WHERE id=1 FOR UPDATE"), 0, func(context.Context, analyze.ParameterInfo, []sqltype.Value, sqltype.InputEnvironment) error {
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	producer := portal.producer
	if producer == nil {
		t.Fatal("missing retained marked query")
	}
	defer producer.dispose(context.Background())
	if scope.QueryLockContext != locks || producer.locks != locks || locks.IdentityOnly {
		t.Fatal("mark initialization replaced or failed to promote the shared owner")
	}
	if len(locks.Leaves) != 5 {
		t.Fatal("missing four policy-helper and one target physical occurrences", len(locks.Leaves))
	}
	for i, leaf := range locks.Leaves {
		if leaf.Source == nil || leaf.Source.Locks != locks || leaf.Source.Occurrence != i || leaf.Mark.Mode == 0 {
			t.Fatal("physical occurrence lost its shared identity", i, leaf)
		}
	}
	if len(producer.rlsPrograms) != 1 || len(producer.rlsPrograms[0].Groups) != 2 {
		t.Fatal("missing distinct UPDATE and SELECT command groups")
	}
	physical := 0
	for _, group := range producer.rlsPrograms[0].Groups {
		for _, clauses := range [][]exec.RLSRuntimeClause{group.Permissive, group.Restrictive} {
			for _, clause := range clauses {
				if clause.State.Source == nil || !clause.State.Locking || clause.State.Source.Locks != locks || clause.State.Source.Occurrence != physical {
					t.Fatal("clause lost its physical occurrence", physical)
				}
				slots := 0
				var query func(plan.Node)
				query = func(node plan.Node) {
					if node == nil {
						return
					}
					if source, ok := node.(*plan.SourceNode); ok {
						slots++
						if source.Occurrence != 0 {
							t.Fatal("private query slot confused with global physical occurrence", source.Occurrence)
						}
						return
					}
					left, right := plan.QueryInputs(node)
					query(left)
					query(right)
				}
				var expression func(expr.Expr)
				expression = func(node expr.Expr) {
					if subquery, ok := node.(plan.SubqueryExpr); ok {
						query(subquery.QueryPlan.Root)
					}
					plan.ExpressionChildren(node, expression)
				}
				expression(clause.Expression)
				if slots != 1 {
					t.Fatal("unexpected imported helper query shape", slots)
				}
				physical++
			}
		}
	}
	// Repeated marked and unmarked initialization validates afresh without
	// discarding previously assigned occurrence IDs or the shared leaves.
	e.Mu.Lock()
	err = e.InitializeQueryLocksLocked(scope, nil, scope.QuerySources)
	if err == nil {
		err = e.InitializeQueryLocksLocked(scope, nil, nil)
	}
	e.Mu.Unlock()
	if err != nil || scope.QueryLockContext != locks || len(locks.Leaves) != 5 || locks.IdentityOnly {
		t.Fatal("later initialization reset the admitted manifest", err)
	}
	err = scope.Finish(nil)
	finished = true
	if err != nil {
		t.Fatal(err)
	}
	if len(producer.rlsPrograms) != 1 {
		t.Fatal("statement finish released producer-owned marked clauses")
	}
}

func TestRLSFirstPolicyMark(t *testing.T) {
	e, op, done := rlsCursorFixture(t)
	defer done()
	roleExecute(t, e, op, "RESET ROLE")
	roleExecute(t, e, op, "GRANT UPDATE ON helper TO rls_cursor_reader")
	roleExecute(t, e, op, "ALTER POLICY p ON t USING(EXISTS(SELECT 1 FROM helper WHERE helper.id=t.id FOR SHARE))")
	roleExecute(t, e, op, "SET ROLE rls_cursor_reader")
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	finished := false
	defer func() {
		if !finished {
			_ = scope.Finish(context.Canceled)
		}
	}()
	portal := &protocolPortal{}
	scope.protocol().bind = portal
	_, err = scope.Execute(statement(t, "SELECT t.id FROM helper AS outer_helper JOIN t ON outer_helper.id=t.id"), 0, func(context.Context, analyze.ParameterInfo, []sqltype.Value, sqltype.InputEnvironment) error {
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	producer := portal.producer
	if producer == nil {
		t.Fatal("missing retained source bundle")
	}
	defer producer.dispose(context.Background())
	locks := scope.QueryLockContext
	if locks == nil || locks.IdentityOnly || len(locks.Leaves) != 3 {
		t.Fatal("later policy source did not establish the shared physical manifest")
	}
	for i, leaf := range locks.Leaves {
		if leaf.Source == nil || leaf.Source.Locks != locks || leaf.Source.Occurrence != i {
			t.Fatal("first policy mark left an earlier outer source without its physical identity", i)
		}
	}
	if len(producer.Source.Sources) != 2 {
		t.Fatal("unexpected outer local source manifest")
	}
	for i, source := range producer.Source.Sources {
		if source.Occurrence != i+1 || source.Locks != locks {
			t.Fatal("outer source confused private and shared occurrence slots", i)
		}
	}
	err = scope.Finish(nil)
	finished = true
	if err != nil {
		t.Fatal(err)
	}
}
