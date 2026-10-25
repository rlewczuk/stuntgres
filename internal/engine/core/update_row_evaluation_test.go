package core_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func updateRowTestAction(t *testing.T, e *core.Engine, q *plan.Query) *analyze.UpdateBinding {
	t.Helper()
	source := subqueryTestExpression(t, e, q, parser.SubqueryScalar)
	source.UpdateWidth = 2
	source.Property.StageDependent = true
	return &analyze.UpdateBinding{Assignments: []expr.Expr{source, plan.UpdateOutputExpr{Info: expr.Info{Usage: q.Root.Schema()[1].Usage, Property: expr.Properties{StageDependent: true}}, Index: 1}}, Groups: []analyze.AssignmentGroup{{Start: 0, Width: 2}}, OutputWidth: 2}
}

func TestUpdateRowEvaluation(t *testing.T) {
	t.Run("one_source_fresh_calls", func(t *testing.T) {
		e, q, source := queryJoinsFixture(t, "SELECT k,k+100 FROM a WHERE k=2")
		project := q.Root.(*plan.ProjectNode)
		calls := 0
		subqueryTestTarget(t, project, queryProbe(project.Schema()[0].Usage, func(c *exec.Context) (sqltype.Value, error) {
			calls++
			return subqueryTestInteger(t, project.Schema()[0].Usage.Type, int64(calls*10)), nil
		}))
		state := exec.State{Source: source}
		defer state.ReleaseMemberships()
		c := state.Evaluation(context.Background())
		action := updateRowTestAction(t, e, q)
		old := []sqltype.Value{subqueryTestInteger(t, project.Schema()[0].Usage.Type, 1), subqueryTestInteger(t, project.Schema()[1].Usage.Type, 2)}
		c.Row = old
		prior := []sqltype.Value{old[0]}
		c.UpdateOutputs, c.UpdateOutputsReady = prior, true
		for _, want := range []int64{10, 20, 30} {
			row, err := dml.EvaluateUpdateAssignments(&c, action, []int{0, 1}, old)
			if err != nil || enginetest.Export(row[0]) != want || enginetest.Export(row[1]) != int64(102) || enginetest.Export(old[0]) != int64(1) || enginetest.Export(old[1]) != int64(2) {
				t.Fatal(row, old, err)
			}
			if !c.UpdateOutputsReady || !reflect.DeepEqual(c.UpdateOutputs, prior) {
				t.Fatal("owner not restored", c.UpdateOutputs, c.UpdateOutputsReady)
			}
		}
		if calls != 3 {
			t.Fatal("source reevaluated for each output", calls)
		}
	})
	t.Run("zero_and_second_row", func(t *testing.T) {
		for _, predicate := range []string{"k=9", "k=1"} {
			e, q, source := queryJoinsFixture(t, "SELECT k,k+100 FROM a WHERE "+predicate)
			execution := exec.State{Source: source}
			c := execution.Evaluation(context.Background())
			action := updateRowTestAction(t, e, q)
			typ := q.Root.Schema()[0].Usage.Type
			old := []sqltype.Value{subqueryTestInteger(t, typ, 7), subqueryTestInteger(t, typ, 8)}
			c.Row = old
			row, err := dml.EvaluateUpdateAssignments(&c, action, []int{0, 1}, old)
			if predicate == "k=1" {
				if row != nil {
					t.Fatal("cardinality returned tuple", row)
				}
				enginetest.State(t, err, "21000")
			} else if err != nil || !row[0].IsNull() || !row[1].IsNull() || row[0].Type() != typ || row[1].Type() != typ {
				t.Fatal(row, err)
			}
			if c.UpdateOutputs != nil || c.UpdateOutputsReady {
				t.Fatal("tuple owner leaked after exit")
			}
			execution.ReleaseMemberships()
		}
	})
	t.Run("groups_and_nested_owner", func(t *testing.T) {
		e, q, source := queryJoinsFixture(t, "SELECT k,k+100 FROM a WHERE k=2")
		project := q.Root.(*plan.ProjectNode)
		calls := 0
		subqueryTestTarget(t, project, queryProbe(project.Schema()[0].Usage, func(c *exec.Context) (sqltype.Value, error) {
			calls++
			return sqltype.Integer(project.Schema()[0].Usage.Type, int64(calls*10))
		}))
		execution := exec.State{Source: source}
		defer execution.ReleaseMemberships()
		c := execution.Evaluation(context.Background())
		action := updateRowTestAction(t, e, q)
		second := action.Assignments[1].(plan.UpdateOutputExpr)
		second.Group = 2
		action.Assignments = append(action.Assignments, action.Assignments[0], second)
		action.Groups = append(action.Groups, analyze.AssignmentGroup{Start: 2, Width: 2})
		typ := project.Schema()[0].Usage.Type
		old := []sqltype.Value{subqueryTestInteger(t, typ, 1), subqueryTestInteger(t, typ, 2), subqueryTestInteger(t, typ, 3), subqueryTestInteger(t, typ, 4)}
		// Simulate a live outer tuple using the exact same context scratch.
		outer := []sqltype.Value{old[0], old[1]}
		c.UpdateOutputs, c.UpdateOutputScratch, c.UpdateOutputsReady = outer, outer, true
		row, err := dml.EvaluateUpdateAssignments(&c, action, []int{0, 1, 2, 3}, old)
		if err != nil || calls != 2 || enginetest.Export(row[0]) != int64(10) || enginetest.Export(row[1]) != int64(102) || enginetest.Export(row[2]) != int64(20) || enginetest.Export(row[3]) != int64(102) {
			t.Fatal(row, calls, err)
		}
		if !c.UpdateOutputsReady || !reflect.DeepEqual(c.UpdateOutputs, outer) || enginetest.Export(outer[0]) != int64(1) || enginetest.Export(outer[1]) != int64(2) || &c.UpdateOutputScratch[0] != &outer[0] {
			t.Fatal("nested owner overwrote outer tuple")
		}
	})
	t.Run("constructor_old_frame", func(t *testing.T) {
		e := engine.New()
		typ, _ := e.CatalogRead().LookupType(sqltype.Int4)
		old := []sqltype.Value{subqueryTestInteger(t, typ, 10), subqueryTestInteger(t, typ, 20)}
		c := exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}, EvalFrame: exec.EvalFrame{Row: old}}
		calls := [2]int{}
		action := &analyze.UpdateBinding{Assignments: []expr.Expr{
			queryProbe(typ.Usage(), func(c *exec.Context) (sqltype.Value, error) { calls[0]++; return c.Row[1], nil }),
			queryProbe(typ.Usage(), func(c *exec.Context) (sqltype.Value, error) { calls[1]++; return c.Row[0], nil }),
		}}
		row, err := dml.EvaluateUpdateAssignments(&c, action, []int{0, 1}, old)
		if err != nil || enginetest.Export(row[0]) != int64(20) || enginetest.Export(row[1]) != int64(10) || calls != [2]int{1, 1} {
			t.Fatal(row, calls, err)
		}
	})
}

func TestUpdateRowCancellationBeforeFinish(t *testing.T) {
	for _, phase := range []string{"live_source", "finish", "late_cardinality"} {
		t.Run(phase, func(t *testing.T) {
			e := engine.New()
			owner := sessionStart(t, e, nil)
			peer := sessionStart(t, e, nil)
			sessionSQL(t, peer, "CREATE TABLE t(id INT,a INT,b INT)")
			sessionSQL(t, peer, "INSERT INTO t VALUES(1,10,20),(2,30,40)")
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
			subqueryTestTarget(t, project, queryProbe(project.Schema()[0].Usage, func(c *exec.Context) (sqltype.Value, error) {
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
			transactionRows(t, peer, "SELECT a FROM t ORDER BY id", 10, 30)
			transactionRows(t, peer, "SELECT b FROM t ORDER BY id", 20, 40)
			enginetest.LockEmpty(t, e)
			sessionSQL(t, owner, "UPDATE t SET (a,b)=(SELECT a+1,b+2)")
			transactionRows(t, peer, "SELECT a FROM t ORDER BY id", 11, 31)
			transactionRows(t, peer, "SELECT b FROM t ORDER BY id", 22, 42)
		})
	}
}
