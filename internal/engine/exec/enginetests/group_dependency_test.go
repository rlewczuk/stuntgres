package enginetests_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func groupDependencyExecutionFixture(t *testing.T, e *core.Engine, table, sql string, physical bool) (*plan.Query, *exec.Source, *plan.AggregateNode) {
	t.Helper()
	statement := enginetest.Statement(t, sql)
	op, done, err := enginetest.Operation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		e.Mu.Unlock()
		done()
		t.Fatal(err)
	}
	binding, err := core.Describe(op.Ctx, e.AnalysisHost(op), statement, auth, ids.ObjectRef{}, sqltype.InputEnvironment{}, nil, nil)
	e.Mu.Unlock()
	done()
	if err != nil {
		t.Fatal(err)
	}
	var source *exec.Source
	if physical {
		op, done, failure := enginetest.Operation(e, context.Background())
		if failure != nil {
			t.Fatal(failure)
		}
		t.Cleanup(done)
		scope, failure := e.StartStatement(op)
		if failure != nil {
			t.Fatal(failure)
		}
		t.Cleanup(func() {
			if failure := scope.Finish(nil); failure != nil {
				t.Error(failure)
			}
		})
		ready, failure := scope.Execute(enginetest.Statement(t, "SELECT 1"), 0, nil)
		if ready.Rows != nil {
			ready.Rows.Close()
		}
		if failure != nil {
			t.Fatal(failure)
		}
		view, failure := scope.AcquireDataView(binding.QuerySources[0].Ref)
		if failure != nil {
			t.Fatal(failure)
		}
		source, err = exec.CaptureSource(t.Context(), view, binding.QuerySources[0].Ref)
		if err != nil {
			t.Fatal(err)
		}
	} else {
		captured := enginetest.Run(t, e, "SELECT * FROM "+table).Rows.(*exec.FinalRows)
		source = captured.Source
	}
	t.Cleanup(source.Close)
	var aggregate *plan.AggregateNode
	plan.VisitCTEQuery(binding.Query.Root, func(node plan.Node) {
		if found, ok := node.(*plan.AggregateNode); ok {
			aggregate = found
		}
	}, func(expr.Expr) {}, true)
	if aggregate == nil || len(aggregate.Dependent) == 0 {
		t.Fatal("missing real accepted dependent aggregation")
	}
	return binding.Query, source, aggregate
}

func groupDependencyExecutionRows(t *testing.T, rows exec.RowResult) [][]sqltype.Value {
	t.Helper()
	defer rows.Close()
	var out [][]sqltype.Value
	for {
		row := make([]sqltype.Value, len(rows.Schema()))
		present, err := rows.Next(t.Context(), row)
		if err != nil {
			t.Fatal(err)
		}
		if !present {
			return out
		}
		out = append(out, row)
	}
}

func TestGroupDependencyAggregateExecution(t *testing.T) {
	e := engine.New()
	for _, sql := range []string{
		"CREATE TABLE gd_exec(id int PRIMARY KEY,payload json,s text)",
		"CREATE TABLE gd_exec_child(extra int) INHERITS(gd_exec)",
		`INSERT INTO gd_exec VALUES(1,'{"source":"parent"}','parent'),(2,NULL,NULL)`,
		`INSERT INTO gd_exec_child VALUES(1,'{"source":"child"}','child',7)`,
	} {
		enginetest.Run(t, e, sql)
	}
	query, source, aggregate := groupDependencyExecutionFixture(t, e, "gd_exec", "SELECT id,payload,s,count(*) FROM gd_exec GROUP BY id ORDER BY id", false)
	if len(aggregate.Keys) != 1 || len(aggregate.Grouping) != 1 || len(aggregate.Dependent) != 2 || aggregate.DependentStart() != 3 {
		t.Fatal("representative payload entered the grouping key or shifted call slots")
	}
	rows, count, err := exec.Finalize(t.Context(), query.Root, source, nil)
	if err != nil || count != 2 {
		t.Fatal("different inherited payload split a same-key group", count, err)
	}
	values := groupDependencyExecutionRows(t, rows)
	first, err := values[0][1].JSONText()
	if err != nil || first != `{"source":"parent"}` {
		t.Fatal("first native input representative not retained", first, err)
	}
	payload, _ := values[0][2].Text()
	combined, _ := values[0][3].Integer()
	if payload != "parent" || combined != 2 || !values[1][1].IsNull() || !values[1][2].IsNull() {
		t.Fatal("inherited count, JSON or NULL retention changed", values)
	}
	if single, _ := values[1][3].Integer(); single != 1 {
		t.Fatal("independent group count changed", single)
	}
	t.Run("cancel_during_first_representative", func(t *testing.T) {
		query, source, aggregate := groupDependencyExecutionFixture(t, e, "gd_exec", "SELECT id,payload,count(*) FROM gd_exec GROUP BY id", false)
		original := aggregate.Dependent[0]
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		var owner *exec.State
		aggregate.Dependent = append([]expr.Expr(nil), aggregate.Dependent...)
		aggregate.Dependent[0] = enginetest.QueryProbe(original.ResultUsage(), func(evaluation *exec.Context) (sqltype.Value, error) {
			owner = evaluation.Query
			value, err := original.Eval(evaluation)
			cancel()
			return value, err
		})
		result, count, err := exec.Finalize(ctx, query.Root, source, nil)
		if !errors.Is(err, context.Canceled) || result != nil || count != 0 || owner == nil {
			t.Fatal("canceled representative published a partial result", count, err)
		}
		if source.View != nil || source.Sources != nil || source.Inheritance != nil || owner.Source.View != nil {
			t.Fatal("canceled aggregate retained child/group input resources")
		}
	})
	t.Run("empty_keyed_input", func(t *testing.T) {
		enginetest.Run(t, e, "CREATE TABLE gd_empty(id int PRIMARY KEY,payload json)")
		query, source, _ := groupDependencyExecutionFixture(t, e, "gd_empty", "SELECT id,payload,count(*) FROM gd_empty GROUP BY id", true)
		rows, count, err := exec.Finalize(t.Context(), query.Root, source, nil)
		if err != nil || count != 0 {
			t.Fatal("empty keyed input invented a representative", count, err)
		}
		if values := groupDependencyExecutionRows(t, rows); len(values) != 0 {
			t.Fatal("empty grouped output is nonempty")
		}
	})
}

func TestGroupDependencyParallelPayload(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE TABLE gd_parallel(id int PRIMARY KEY,payload json,s text)")
	enginetest.Run(t, e, `INSERT INTO gd_parallel VALUES(1,'{"x":1}','one'),(2,NULL,NULL),(3,'{"x":3}','three'),(4,'{"x":4}','four')`)
	sql := "SELECT id,payload,s,string_agg(s,',') FROM gd_parallel GROUP BY id ORDER BY id"
	serialQuery, serialSource, _ := groupDependencyExecutionFixture(t, e, "gd_parallel", sql, true)
	serialRows, serialCount, err := exec.Finalize(t.Context(), serialQuery.Root, serialSource, nil)
	if err != nil || serialCount != 4 {
		t.Fatal("serial aggregate failed", serialCount, err)
	}
	groupDependencyAssertPayloadRows(t, groupDependencyExecutionRows(t, serialRows))
	query, source, aggregate := groupDependencyExecutionFixture(t, e, "gd_parallel", sql, true)
	source.PlannerCosts = plan.DefaultPlannerCosts()
	source.PlannerCosts.ParallelSetup, source.PlannerCosts.ParallelTuple, source.PlannerCosts.MinParallelTableBlocks = 0, 0, 0
	source.PlannerRows, source.PlannerColumns = 1000, 3
	admitted, eligible := exec.ParallelAggregatePlan(query.Root, source)
	if !eligible || admitted != aggregate {
		t.Fatal("real partial-capable provider/payload was denied worker admission")
	}
	// Observe real bound evaluation, not a replacement aggregate or echoed row.
	var mu sync.Mutex
	workers := make(map[*exec.State]int)
	original := aggregate.Dependent[0]
	aggregate.Dependent = append([]expr.Expr(nil), aggregate.Dependent...)
	aggregate.Dependent[0] = enginetest.QueryProbe(original.ResultUsage(), func(evaluation *exec.Context) (sqltype.Value, error) {
		if !exec.InParallelWorker(evaluation.Context) {
			return sqltype.Value{}, errors.New("dependent payload evaluated outside its worker")
		}
		mu.Lock()
		workers[evaluation.Query]++
		mu.Unlock()
		return original.Eval(evaluation)
	})
	rows, count, err := exec.FinalizeParallelAggregate(t.Context(), query.Root, aggregate, source, nil, 2)
	if err != nil || count != 4 {
		t.Fatal("actual worker aggregation failed", count, err)
	}
	values := groupDependencyExecutionRows(t, rows)
	groupDependencyAssertPayloadRows(t, values)
	if len(workers) != 2 {
		t.Fatal("did not exercise two genuine partial worker owners", len(workers))
	}
	for worker, groups := range workers {
		if groups != 2 || worker.Source.View != nil {
			t.Fatal("worker payload demand or input lifetime changed", groups)
		}
	}
}

func groupDependencyAssertPayloadRows(t *testing.T, values [][]sqltype.Value) {
	t.Helper()
	if len(values) != 4 {
		t.Fatal("wrong representative group count", len(values))
	}
	wantText := []string{"one", "", "three", "four"}
	wantJSON := []string{`{"x":1}`, "", `{"x":3}`, `{"x":4}`}
	for i, row := range values {
		id, _ := row[0].Integer()
		if id != int64(i+1) {
			t.Fatal("result key changed", row)
		}
		if i == 1 {
			if !row[1].IsNull() || !row[2].IsNull() || !row[3].IsNull() {
				t.Fatal("NULL representative changed", row)
			}
			continue
		}
		json, jsonErr := row[1].JSONText()
		payload, payloadErr := row[2].Text()
		combined, combineErr := row[3].Text()
		if jsonErr != nil || payloadErr != nil || combineErr != nil || json != wantJSON[i] || payload != wantText[i] || combined != wantText[i] {
			t.Fatal("representative payload or unchanged call slot changed", row, jsonErr, payloadErr, combineErr)
		}
	}
}
