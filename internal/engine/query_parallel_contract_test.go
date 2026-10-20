package engine

import (
	"context"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/sqltype"
)

func parallelTransactionOccurrences(t *testing.T) {
	t.Helper()
	e, query, _ := queryCoreFixture(t, "SELECT id FROM t")
	s := sessionStart(t, e, nil)
	for _, mode := range []string{"update-delete", "truncate"} {
		t.Run(mode, func(t *testing.T) {
			sessionSQL(t, s, "BEGIN")
			want := map[int64]int{3: 1, 4: 1, 5: 1, 9: 1}
			if mode == "update-delete" {
				sessionSQL(t, s, "UPDATE t SET id=9 WHERE id=1")
				sessionSQL(t, s, "DELETE FROM t WHERE id=2")
				sessionSQL(t, s, "INSERT INTO t VALUES(5,5,TRUE)")
			} else {
				sessionSQL(t, s, "TRUNCATE t")
				sessionSQL(t, s, "INSERT INTO t VALUES(7,NULL,NULL),(8,NULL,NULL)")
				want = map[int64]int{7: 1, 8: 1}
			}
			captured := sessionSQL(t, s, "SELECT id FROM t").Rows.(*finalRows)
			input, err := captureQuerySource(context.Background(), captured.source.view, captured.source.ref)
			if err != nil {
				t.Fatal(err)
			}
			captured.Close()
			if _, _, ok := parallelQueryPlan(query.Root, input); !ok {
				t.Fatal("selected own-write physical scan not eligible")
			}
			rows, n, err := finalizeSelectQuery(context.Background(), query.Root, input, nil, 2)
			if err != nil || n != len(want) {
				t.Fatal(n, err)
			}
			defer rows.Close()
			sessionSQL(t, s, "ROLLBACK")
			got := map[int64]int{}
			for _, id := range parallelTestIDs(t, rows) {
				got[id]++
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatal(got, want)
			}
			fresh := sessionSQL(t, s, "SELECT id FROM t ORDER BY id")
			if got := parallelTestIDs(t, fresh.Rows); !reflect.DeepEqual(got, []int64{1, 2, 3, 4}) {
				t.Fatal("rolled-back heads became visible", got)
			}
		})
	}
}

func parallelAdmissionAndTemporary(t *testing.T) {
	t.Helper()
	e, query, _ := queryCoreFixture(t, "SELECT id FROM t")
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "SET max_parallel_workers_per_gather=7")
	op, _, err := s.AcquireCommand(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if op.parallelWorkers != 7 {
		op.Done()
		t.Fatal("missing admission snapshot", op.parallelWorkers)
	}
	// The command consumes its immutable value, not live settings storage.
	s.settings.mu.Lock()
	s.settings.values["max_parallel_workers_per_gather"] = "2"
	s.settings.typed["max_parallel_workers_per_gather"] = settings.Value{Text: "2", Number: 2}
	s.settings.mu.Unlock()
	if op.parallelWorkers != 7 {
		op.Done()
		t.Fatal("admitted worker ceiling changed")
	}
	op.Done()
	next, _, err := s.AcquireCommand(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if next.parallelWorkers != 2 {
		next.Done()
		t.Fatal("subsequent admission missed setting change")
	}
	next.Done()
	sessionSQL(t, s, "CREATE TEMP TABLE parallel_temp(id INT)")
	sessionSQL(t, s, "INSERT INTO parallel_temp VALUES(1),(2)")
	captured := sessionSQL(t, s, "SELECT id FROM parallel_temp").Rows.(*finalRows)
	defer captured.Close()
	project := *query.Root.(*plan.ProjectNode)
	project.Child = &plan.SourceNode{QuerySchema: captured.Schema(), Ref: captured.source.ref, Occurrence: captured.source.occurrence}
	if _, _, ok := parallelQueryPlan(&project, captured.source); ok {
		t.Fatal("temporary physical source admitted")
	}
}

func parallelCachedOrderProof(t *testing.T) {
	t.Helper()
	_, query, source := queryCoreFixture(t, "SELECT id+1 AS result FROM t ORDER BY v+1 NULLS FIRST,id")
	project := query.Root.(*plan.ProjectNode)
	ordered := project.Child.(*plan.SortNode)
	var keyCalls, outputCalls atomic.Int64
	key := ordered.Keys[0].Expression
	ordered.Keys[0].Expression = queryProbe(key.ResultUsage(), func(c *EvalContext) (sqltype.Value, error) {
		keyCalls.Add(1)
		return key.Eval(c)
	})
	if len(ordered.Outputs) != 1 {
		t.Fatal("expected scheduled target")
	}
	output := ordered.Outputs[0]
	ordered.Outputs[0] = queryProbe(output.ResultUsage(), func(c *EvalContext) (sqltype.Value, error) {
		outputCalls.Add(1)
		return output.Eval(c)
	})
	rows, n, err := finalizeParallelQuery(context.Background(), project, ordered, source, nil, 2)
	if err != nil || n != 4 {
		t.Fatal(n, err)
	}
	if got := parallelTestIDs(t, rows); !reflect.DeepEqual(got, []int64{3, 2, 5, 4}) {
		t.Fatal(got)
	}
	if keyCalls.Load() != 4 || outputCalls.Load() != 4 {
		t.Fatal("cached ORDER/target reevaluated", keyCalls.Load(), outputCalls.Load())
	}
}

func parallelMergeCancellationProof(t *testing.T) {
	t.Helper()
	_, query, source := queryCoreFixture(t, "SELECT id FROM t ORDER BY id")
	project := query.Root.(*plan.ProjectNode)
	ordered := project.Child.(*plan.SortNode)
	var runs []queryParallelRun
	for i := range 2 {
		run, err := runParallelQuery(context.Background(), project, source, nil, i*2, (i+1)*2, sqltype.InputEnvironment{}, sqltype.OutputEnvironment{}, true)
		if err != nil {
			t.Fatal(err)
		}
		runs = append(runs, run)
	}
	// Cancellation in the actual leader comparator must veto the whole output.
	ctx := &queryCountingContext{Context: context.Background(), remaining: 3}
	rows, err := gatherQueryRuns(ctx, project, ordered, source, runs, []int{0}, true, sqltype.InputEnvironment{}, sqltype.OutputEnvironment{})
	if rows != nil || !errors.Is(err, context.Canceled) {
		t.Fatal(rows, err)
	}
	for _, pair := range [][2]int{{1, 1}, {0, 1}} {
		// Both-NULL and one-NULL shortcuts must still observe cancellation.
		key := ordered.Keys[0]
		key.Slot = 1
		ctx := &queryCountingContext{Context: context.Background(), remaining: 2}
		_, err := compareQueryOrder(ctx, []plan.SortKey{key}, sqltype.InputEnvironment{}, querySortRow{source: source, index: pair[0]}, querySortRow{source: source, index: pair[1]})
		if !errors.Is(err, context.Canceled) {
			t.Fatal("NULL shortcut ignored cancellation", err)
		}
	}
	// No rows are needed to reach the final publication fence.
	ctx = &queryCountingContext{Context: context.Background(), remaining: 1}
	rows, err = gatherQueryRuns(ctx, project, nil, source, nil, []int{0}, true, sqltype.InputEnvironment{}, sqltype.OutputEnvironment{})
	if rows != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("final publication fence ignored cancellation", rows, err)
	}
}

func parallelOpenFailureProof(t *testing.T) {
	t.Helper()
	_, query, source := queryCoreFixture(t, "SELECT id FROM t")
	project := query.Root.(*plan.ProjectNode)
	project.Child.(*plan.SourceNode).Ref = ids.ObjectRef{}
	rows, n, err := finalizeParallelQuery(context.Background(), project, nil, source, nil, 2)
	if rows != nil || n != 0 || err == nil || err.Error() != "stuntgres: invalid query source target" || source.view != nil {
		t.Fatal("failed worker open exposed output/retained pin", rows, n, err)
	}
}

// Shared write/nested finalizers and retained cursor producers have no positive
// SELECT permission. Merely opening their physical scan never launches workers.
func parallelSerialEntryProof(t *testing.T) {
	t.Helper()
	_, query, source := queryCoreFixture(t, "SELECT id FROM t")
	project := query.Root.(*plan.ProjectNode)
	calls := 0
	project.Expressions[0] = queryProbe(project.Schema()[0].Usage, func(c *EvalContext) (sqltype.Value, error) {
		if c.query.executionContext != nil || c.query.scanRange {
			return sqltype.Value{}, errors.New("generic producer dispatched a worker")
		}
		calls++
		return c.Row[0], nil
	})
	state := &queryState{source: source}
	stage, err := openQueryNode(context.Background(), state, project)
	if err != nil {
		t.Fatal(err)
	}
	defer stage.Close()
	if calls != 0 {
		t.Fatal("retained producer evaluated at open")
	}
	dst := make([]sqltype.Value, 1)
	if present, err := stage.Next(context.Background(), dst); !present || err != nil || calls != 1 {
		t.Fatal("serial demand frontier", present, err, calls)
	}
	stage.Close()
	calls = 0
	rows, n, err := finalizeQuery(context.Background(), query.Root, source, nil)
	if err != nil || n != 4 || calls != 4 {
		t.Fatal("generic finalizer stopped being serial", n, err, calls)
	}
	if got := parallelTestIDs(t, rows); !reflect.DeepEqual(got, []int64{1, 2, 3, 4}) || calls != 4 {
		t.Fatal("final output resumed evaluator", got, calls)
	}
}
