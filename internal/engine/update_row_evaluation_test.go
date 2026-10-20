package engine

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func updateRowTestAction(t *testing.T, e *Engine, q *BoundQuery) *boundUpdate {
	t.Helper()
	source := subqueryTestExpression(t, e, q, parser.SubqueryScalar)
	source.UpdateWidth = 2
	source.Property.StageDependent = true
	return &boundUpdate{assignments: []expr.Expr{source, updateOutputExpression{Info: expr.Info{Usage: q.Root.Schema()[1].Usage, Property: expr.Properties{StageDependent: true}}, Index: 1}}, groups: []updateAssignmentGroup{{start: 0, width: 2}}, outputWidth: 2}
}

func TestUpdateRowEvaluation(t *testing.T) {
	t.Run("one_source_fresh_calls", func(t *testing.T) {
		e, q, source := queryJoinsFixture(t, "SELECT k,k+100 FROM a WHERE k=2")
		project := q.Root.(*projectNode)
		calls := 0
		subqueryTestTarget(t, project, queryProbe(project.Schema()[0].Usage, func(c *EvalContext) (sqltype.Value, error) {
			calls++
			return subqueryTestInteger(t, project.Schema()[0].Usage.Type, int64(calls*10)), nil
		}))
		state := queryState{source: source}
		defer state.releaseMemberships()
		c := state.evaluation(context.Background())
		action := updateRowTestAction(t, e, q)
		old := []sqltype.Value{subqueryTestInteger(t, project.Schema()[0].Usage.Type, 1), subqueryTestInteger(t, project.Schema()[1].Usage.Type, 2)}
		c.Row = old
		prior := []sqltype.Value{old[0]}
		c.updateOutputs, c.updateOutputsReady = prior, true
		for _, want := range []int64{10, 20, 30} {
			row, err := evaluateUpdateAssignments(&c, action, []int{0, 1}, old)
			if err != nil || testExport(row[0]) != want || testExport(row[1]) != int64(102) || testExport(old[0]) != int64(1) || testExport(old[1]) != int64(2) {
				t.Fatal(row, old, err)
			}
			if !c.updateOutputsReady || !reflect.DeepEqual(c.updateOutputs, prior) {
				t.Fatal("owner not restored", c.updateOutputs, c.updateOutputsReady)
			}
		}
		if calls != 3 {
			t.Fatal("source reevaluated for each output", calls)
		}
	})
	t.Run("zero_and_second_row", func(t *testing.T) {
		for _, predicate := range []string{"k=9", "k=1"} {
			e, q, source := queryJoinsFixture(t, "SELECT k,k+100 FROM a WHERE "+predicate)
			execution := queryState{source: source}
			c := execution.evaluation(context.Background())
			action := updateRowTestAction(t, e, q)
			typ := q.Root.Schema()[0].Usage.Type
			old := []sqltype.Value{subqueryTestInteger(t, typ, 7), subqueryTestInteger(t, typ, 8)}
			c.Row = old
			row, err := evaluateUpdateAssignments(&c, action, []int{0, 1}, old)
			if predicate == "k=1" {
				if row != nil {
					t.Fatal("cardinality returned tuple", row)
				}
				state(t, err, "21000")
			} else if err != nil || !row[0].IsNull() || !row[1].IsNull() || row[0].Type() != typ || row[1].Type() != typ {
				t.Fatal(row, err)
			}
			if c.updateOutputs != nil || c.updateOutputsReady {
				t.Fatal("tuple owner leaked after exit")
			}
			execution.releaseMemberships()
		}
	})
	t.Run("groups_and_nested_owner", func(t *testing.T) {
		e, q, source := queryJoinsFixture(t, "SELECT k,k+100 FROM a WHERE k=2")
		project := q.Root.(*projectNode)
		calls := 0
		subqueryTestTarget(t, project, queryProbe(project.Schema()[0].Usage, func(c *EvalContext) (sqltype.Value, error) {
			calls++
			return sqltype.Integer(project.Schema()[0].Usage.Type, int64(calls*10))
		}))
		execution := queryState{source: source}
		defer execution.releaseMemberships()
		c := execution.evaluation(context.Background())
		action := updateRowTestAction(t, e, q)
		second := action.assignments[1].(updateOutputExpression)
		second.Group = 2
		action.assignments = append(action.assignments, action.assignments[0], second)
		action.groups = append(action.groups, updateAssignmentGroup{start: 2, width: 2})
		typ := project.Schema()[0].Usage.Type
		old := []sqltype.Value{subqueryTestInteger(t, typ, 1), subqueryTestInteger(t, typ, 2), subqueryTestInteger(t, typ, 3), subqueryTestInteger(t, typ, 4)}
		// Simulate a live outer tuple using the exact same context scratch.
		outer := []sqltype.Value{old[0], old[1]}
		c.updateOutputs, c.updateOutputScratch, c.updateOutputsReady = outer, outer, true
		row, err := evaluateUpdateAssignments(&c, action, []int{0, 1, 2, 3}, old)
		if err != nil || calls != 2 || testExport(row[0]) != int64(10) || testExport(row[1]) != int64(102) || testExport(row[2]) != int64(20) || testExport(row[3]) != int64(102) {
			t.Fatal(row, calls, err)
		}
		if !c.updateOutputsReady || !reflect.DeepEqual(c.updateOutputs, outer) || testExport(outer[0]) != int64(1) || testExport(outer[1]) != int64(2) || &c.updateOutputScratch[0] != &outer[0] {
			t.Fatal("nested owner overwrote outer tuple")
		}
	})
	t.Run("constructor_old_frame", func(t *testing.T) {
		e := New()
		typ, _ := e.CatalogRead().LookupType(sqltype.Int4)
		old := []sqltype.Value{subqueryTestInteger(t, typ, 10), subqueryTestInteger(t, typ, 20)}
		c := EvalContext{evalStatement: evalStatement{Context: context.Background()}, evalFrame: evalFrame{Row: old}}
		calls := [2]int{}
		action := &boundUpdate{assignments: []expr.Expr{
			queryProbe(typ.Usage(), func(c *EvalContext) (sqltype.Value, error) { calls[0]++; return c.Row[1], nil }),
			queryProbe(typ.Usage(), func(c *EvalContext) (sqltype.Value, error) { calls[1]++; return c.Row[0], nil }),
		}}
		row, err := evaluateUpdateAssignments(&c, action, []int{0, 1}, old)
		if err != nil || testExport(row[0]) != int64(20) || testExport(row[1]) != int64(10) || calls != [2]int{1, 1} {
			t.Fatal(row, calls, err)
		}
	})
}

func TestUpdateRowCancellationBeforeFinish(t *testing.T) {
	for _, phase := range []string{"live_source", "finish", "late_cardinality"} {
		t.Run(phase, func(t *testing.T) {
			e := New()
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
			source := b.update.assignments[0].(subqueryExpression)
			project := source.QueryPlan.Root.(*projectNode)
			calls, stagedBeforeCancel := 0, false
			subqueryTestTarget(t, project, queryProbe(project.Schema()[0].Usage, func(c *EvalContext) (sqltype.Value, error) {
				calls++
				if phase == "late_cardinality" && calls == 3 {
					stagedBeforeCancel = len(scope.stage.rows[b.ref.Address]) != 0
				}
				if calls == 2 && phase == "live_source" {
					stagedBeforeCancel = len(scope.stage.rows[b.ref.Address]) != 0
					cancel()
					return sqltype.Value{}, ctx.Err()
				}
				return sqltype.Integer(project.Schema()[0].Usage.Type, 99)
			}))
			result, err := e.executeUpdate(ctx, scope, b, target, sources, nil, "public")
			if phase == "live_source" {
				if !errors.Is(err, context.Canceled) || result.Rows != nil || result.Count != 0 || calls != 2 || !stagedBeforeCancel {
					t.Fatal("late source cancellation", result, err, calls, stagedBeforeCancel)
				}
			} else if phase == "late_cardinality" {
				state(t, err, "21000")
				if result.Rows != nil || result.Count != 0 || calls != 3 || !stagedBeforeCancel {
					t.Fatal("cardinality did not discard earlier staged row", result, calls, stagedBeforeCancel)
				}
			} else {
				if err != nil || result.Count != 2 || result.Rows == nil || calls != 2 || len(scope.stage.rows[b.ref.Address]) == 0 {
					t.Fatal("private replacements missing", result, err, calls)
				}
				result.Rows.Close()
				cancel()
			}
			finishErr := scope.Finish(err)
			if phase == "late_cardinality" {
				state(t, finishErr, "21000")
			} else if !errors.Is(finishErr, context.Canceled) {
				t.Fatal("Finish lost cancellation veto", finishErr)
			}
			op.Done()
			transactionRows(t, peer, "SELECT a FROM t ORDER BY id", 10, 30)
			transactionRows(t, peer, "SELECT b FROM t ORDER BY id", 20, 40)
			lockEmpty(t, e)
			sessionSQL(t, owner, "UPDATE t SET (a,b)=(SELECT a+1,b+2)")
			transactionRows(t, peer, "SELECT a FROM t ORDER BY id", 11, 31)
			transactionRows(t, peer, "SELECT b FROM t ORDER BY id", 22, 42)
		})
	}
}
