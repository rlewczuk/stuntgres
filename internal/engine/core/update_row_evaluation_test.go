package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func TestUpdateRowCancellationBeforeFinish(t *testing.T) {
	for _, phase := range []string{"live_source", "finish", "late_cardinality"} {
		t.Run(phase, func(t *testing.T) {
			e := engine.New()
			owner := enginetest.StartSession(t, e, nil)
			peer := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, peer, "CREATE TABLE t(id INT,a INT,b INT)")
			enginetest.SessionSQL(t, peer, "INSERT INTO t VALUES(1,10,20),(2,30,40)")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			q := "UPDATE t SET (a,b)=(SELECT a+1,b+2) RETURNING a,b"
			if phase == "late_cardinality" {
				q = "UPDATE t SET (a,b)=(SELECT a+1,b+2 FROM (VALUES(1),(2)) draw(n) WHERE t.id=2 OR draw.n=1) RETURNING a,b"
			}
			op, scope, b, target, sources := updateFromBoundSources(t, e, owner, ctx, q)
			source := b.Update.Assignments[0].(plan.SubqueryExpr)
			project := source.QueryPlan.Root.(*plan.ProjectNode)
			calls, stagedBeforeCancel := 0, false
			subqueryTestTarget(t, project, enginetest.QueryProbe(project.Schema()[0].Usage, func(c *exec.Context) (sqltype.Value, error) {
				calls++
				if phase == "late_cardinality" && calls == 3 {
					stagedBeforeCancel = len(scope.Stage.Rows[b.Ref.Address]) != 0
				}
				if calls == 2 && phase == "live_source" {
					stagedBeforeCancel = len(scope.Stage.Rows[b.Ref.Address]) != 0
					cancel()
					return sqltype.Value{}, ctx.Err()
				}
				return sqltype.Integer(project.Schema()[0].Usage.Type, 99)
			}))
			result, err := core.ExecuteUpdate(ctx, scope, b, target, sources, nil, "public")
			if phase == "live_source" {
				if !errors.Is(err, context.Canceled) || result.Rows != nil || result.Count != 0 || calls != 2 || !stagedBeforeCancel {
					t.Fatal("late source cancellation", result, err, calls, stagedBeforeCancel)
				}
			} else if phase == "late_cardinality" {
				enginetest.State(t, err, "21000")
				if result.Rows != nil || result.Count != 0 || calls != 3 || !stagedBeforeCancel {
					t.Fatal("cardinality did not discard earlier staged row", result, calls, stagedBeforeCancel)
				}
			} else {
				if err != nil || result.Count != 2 || result.Rows == nil || calls != 2 || len(scope.Stage.Rows[b.Ref.Address]) == 0 {
					t.Fatal("private replacements missing", result, err, calls)
				}
				result.Rows.Close()
				cancel()
			}
			finishErr := scope.Finish(err)
			if phase == "late_cardinality" {
				enginetest.State(t, finishErr, "21000")
			} else if !errors.Is(finishErr, context.Canceled) {
				t.Fatal("Finish lost cancellation veto", finishErr)
			}
			op.Done()
			enginetest.TransactionRows(t, peer, "SELECT a FROM t ORDER BY id", 10, 30)
			enginetest.TransactionRows(t, peer, "SELECT b FROM t ORDER BY id", 20, 40)
			enginetest.LockEmpty(t, e)
			enginetest.SessionSQL(t, owner, "UPDATE t SET (a,b)=(SELECT a+1,b+2)")
			enginetest.TransactionRows(t, peer, "SELECT a FROM t ORDER BY id", 11, 31)
			enginetest.TransactionRows(t, peer, "SELECT b FROM t ORDER BY id", 22, 42)
		})
	}
}
