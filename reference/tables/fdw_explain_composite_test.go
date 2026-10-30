package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceFDWExplainCompositePlans(t *testing.T) {
	fdwExplainCompositeScenario(t)
}

func fdwExplainCompositeScenario(t *testing.T) {
	fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
		r.advanced = &fdwAdvancedState{}
		fdwPushdownLive(t, p, b, r, run)
		sentinel := fdwExplainPlanningSentinel(run)
		unchanged := func() {
			sentinel()
			fdwWriteSourceRows(t, p, b, r, fixture.Select(`SELECT "Key","Value" FROM fdw_source."Left input" ORDER BY "Key" NULLS LAST,"Value" NULLS LAST`, []string{"Key", "Value"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(1), nil}, {int64(2), int64(20)}, {int64(4), int64(40)}, {nil, int64(30)}}))
			fdwWriteSourceRows(t, p, b, r, fixture.Select(`SELECT "Key","Weight" FROM fdw_source."Right input" ORDER BY "Key" NULLS LAST`, []string{"Key", "Weight"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(100)}, {int64(1), int64(100)}, {int64(3), int64(300)}, {nil, int64(900)}}))
		}
		for _, tc := range []struct {
			query, stage string
			columns, types []string
			rows [][]any
			bag bool
		}{
			{"SELECT l.k,l.v,r.w FROM fdw_push_left l JOIN fdw_push_right r ON l.k=r.k", "join", []string{"k", "v", "w"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(10), int64(100)}, {int64(1), int64(10), int64(100)}, {int64(1), nil, int64(100)}, {int64(1), nil, int64(100)}}, true},
			{"SELECT l.k,count(*) AS n,sum(l.v) AS v,sum(r.w) AS w FROM fdw_push_left l JOIN fdw_push_right r ON l.k=r.k GROUP BY l.k", "aggregate", []string{"k", "n", "v", "w"}, []string{"INT4", "INT8", "INT8", "INT8"}, [][]any{{int64(1), int64(4), int64(20), int64(400)}}, false},
			{"SELECT k,v FROM fdw_push_left ORDER BY k NULLS LAST,v NULLS LAST OFFSET 1 LIMIT 2", "limit", []string{"k", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), nil}, {int64(2), int64(20)}}, false},
			{"SELECT l.k,count(*) AS n FROM fdw_push_left l JOIN fdw_push_right r ON l.k=r.k GROUP BY l.k ORDER BY l.k NULLS LAST LIMIT 1", "limit", []string{"k", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(4)}}, false},
		} {
			r.setHook(func(ctx context.Context, method string) error {
				if method == "BeginForeignScan" || method == "BeginForeignQueryScan" || method == "GetForeignRelSize" || method == "GetForeignPaths" || method == "GetForeignPlan" { t.Errorf("replaced base planning/runtime callback during composite EXPLAIN: %s", method) }
				return ctx.Err()
			})
			fdwExplainRoutes(t, p, c, "EXPLAIN (VERBOSE,COSTS false) "+tc.query, func(lines []string) {
				text := strings.Join(lines, "\n")
				if strings.Contains(text, "Actual Rows") || strings.Contains(text, "Provider estimates:") { t.Fatal(text) }
				if !strings.Contains(text, "Remote SQL:") { t.Fatal("missing executable provider SQL", text) }
				if !b.PG && (strings.Count(text, "Foreign Scan") != 1 || strings.Contains(text, "Foreign Scan on")) { t.Fatal("not a single selected composite", text) }
				unchanged()
			})
			r.setHook(nil)
			if !b.PG {
				r.advanced.mu.Lock()
				selected := r.advanced.queryPlans[len(r.advanced.queryPlans)-1]
				r.advanced.mu.Unlock()
				if !fdwQueryContainsStage(selected.Request.Stage, tc.stage) { t.Fatalf("selected %s lacks %s", selected.Request.Stage.Kind, tc.stage) }
				r.queryRequestHook = func(actual stuntgres.ForeignQueryRequest) {
					if !reflect.DeepEqual(fdwQuerySemanticStage(actual.Stage), fdwQuerySemanticStage(selected.Request.Stage)) { t.Error("executed composite differs from explained selection") }
				}
			}
			fdwPushdownValue(run, tc.query, tc.columns, tc.types, tc.rows, tc.bag)
			held := fixture.Select(tc.query, tc.columns, tc.types, tc.rows)
			held.Bag = tc.bag
			fdwPushdownHeld(t, p, b, c, tc.query, held, held)
			if !b.PG {
				r.advanced.mu.Lock()
				selected := r.advanced.queryPlans[len(r.advanced.queryPlans)-1]
				actual := r.advanced.executedPlans[len(r.advanced.executedPlans)-1]
				r.advanced.mu.Unlock()
				if !reflect.DeepEqual(actual, selected.Path.Plan) { t.Fatal("execution plan differs from explained selected plan", actual, selected.Path.Plan) }
			}
			r.queryRequestHook = nil
			costed := strings.Join(fdwExplainText(t,p,c,"EXPLAIN (VERBOSE) "+tc.query),"\n")
			if !b.PG && (!strings.Contains(costed,"Provider estimates: startup cost=0.00 total cost=1.00") || strings.Contains(costed,"rows=") || strings.Contains(costed,"width=")) { t.Fatal("composite fabricated base estimates or lost chosen cost",costed) }
			unchanged()
		}
		query := "SELECT l.k,r.w FROM fdw_push_left l LEFT JOIN fdw_push_right r ON l.k=r.k AND l.v+0<0"
		fdwExplainRoutes(t, p, c, "EXPLAIN (VERBOSE,COSTS false) "+query, func(lines []string) {
			if !b.PG && strings.Count(strings.Join(lines, "\n"), "Foreign Scan on") != 2 { t.Fatal("residual did not retain local fallback", lines) }
			unchanged()
		})
		fdwPushdownValue(run, query, []string{"k", "w"}, []string{"INT4", "INT4"}, [][]any{{int64(1), nil}, {int64(1), nil}, {int64(2), nil}, {nil, nil}, {int64(4), nil}}, true)
		for _, incompatible := range []bool{false, true} {
			r.mu.Lock(); r.queryPushdown = incompatible; r.queryIncompatible = incompatible; r.mu.Unlock()
			query := "SELECT l.k,l.v,r.w FROM fdw_push_left l JOIN fdw_push_right r ON l.k=r.k"
			fdwExplainRoutes(t, p, c, "EXPLAIN (VERBOSE,COSTS false) "+query, func(lines []string) {
				if !b.PG && strings.Count(strings.Join(lines, "\n"), "Foreign Scan on") != 2 { t.Fatal("declined/incompatible composite did not fall back", lines) }
				unchanged()
			})
			fdwPushdownValue(run, query, []string{"k", "v", "w"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(10), int64(100)}, {int64(1), int64(10), int64(100)}, {int64(1), nil, int64(100)}, {int64(1), nil, int64(100)}}, true)
		}
		r.mu.Lock(); r.queryPushdown = true; r.queryIncompatible = false; r.mu.Unlock()
		fdwExplainCompositeBounds(t,p,b,c,r,run,unchanged)
		fdwExplainPlanningContexts(t,p,b,c,r,run,unchanged)
	})
}

func fdwExplainCompositeBounds(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,r *fdwSQLReader,run func(fixture.Step),unchanged func()) {
	t.Helper()
	query := "SELECT k,v FROM fdw_push_left ORDER BY k NULLS LAST,v NULLS LAST LIMIT $1 OFFSET $2"
	explain := "EXPLAIN (VERBOSE,COSTS false) "+query
	statement,err := c.PrepareContext(p.Ctx,explain)
	if err != nil { t.Fatal(err) }; defer statement.Close()
	heldExplain := fixture.HeldStatement{Executor:c,Statement:statement,SQL:explain}
	execution,err := c.PrepareContext(p.Ctx,query)
	if err != nil { t.Fatal(err) }; defer execution.Close()
	heldExecution := fixture.HeldStatement{Executor:c,Statement:execution,SQL:query}
	for _, tc := range []struct { count,offset any; rows [][]any }{
		{int64(2),int64(1),[][]any{{int64(1),nil},{int64(2),int64(20)}}},
		{nil,nil,[][]any{{int64(1),int64(10)},{int64(1),nil},{int64(2),int64(20)},{int64(4),int64(40)},{nil,int64(30)}}},
		{int64(0),int64(0),[][]any{}},
	} {
		before := r.snapshot()
		for _, executor := range []fixture.Executor{c,heldExplain,heldExplain} {
			lines := fdwExplainText(t,p,executor,explain,tc.count,tc.offset)
			if !strings.Contains(strings.Join(lines,"\\n"),"Remote SQL:") { t.Fatal(lines) }
			if !b.PG && r.snapshot().begins != before.begins { t.Fatal("parameter-bound EXPLAIN began a scan") }
			unchanged()
		}
		fresh,err := c.PrepareContext(p.Ctx,explain)
		if err != nil { t.Fatal(err) }
		fdwExplainText(t,p,fixture.HeldStatement{Executor:c,Statement:fresh,SQL:explain},explain,tc.count,tc.offset)
		fresh.Close(); unchanged()
		step := fixture.Select(query,[]string{"k","v"},[]string{"INT4","INT4"},tc.rows,tc.count,tc.offset)
		run(step); run(fixture.Prepared(step))
		fixture.RunStep(t,p.Ctx,heldExecution,b.Recorder,step)
		fixture.RunStep(t,p.Ctx,heldExecution,b.Recorder,step)
		unchanged()
	}
	for _, tc := range []struct{ count,offset int64; code,message string }{
		{-1,0,"2201W","LIMIT must not be negative"},
		{1,-1,"2201X","OFFSET must not be negative"},
	} {
		diagnostic := fdwDiagnostic(tc.code,tc.message)
		if b.PG && tc.count < 0 {
			diagnostic.Context = `remote SQL command: SELECT "Key", "Value" FROM fdw_source."Left input" ORDER BY "Key" ASC NULLS LAST, "Value" ASC NULLS LAST LIMIT (-1)::bigint OFFSET 0::bigint`
		}
		if b.PG && tc.offset < 0 {
			diagnostic.Context = `remote SQL command: SELECT "Key", "Value" FROM fdw_source."Left input" ORDER BY "Key" ASC NULLS LAST, "Value" ASC NULLS LAST LIMIT 1::bigint OFFSET (-1)::bigint`
		}
		step := fixture.ErrorDiagnostic(query,diagnostic,tc.count,tc.offset)
		run(step); run(fixture.Prepared(step))
		diagnostic = fdwDiagnostic(tc.code,tc.message)
		if b.PG {
			diagnostic.Context = `remote SQL command: SELECT "Key", "Value" FROM fdw_source."Left input" ORDER BY "Key" ASC NULLS LAST, "Value" ASC NULLS LAST LIMIT $1::bigint OFFSET $2::bigint`
		}
		fixture.RunStep(t,p.Ctx,heldExecution,b.Recorder,fixture.ErrorDiagnostic(query,diagnostic,tc.count,tc.offset))
		unchanged()
	}
}

func fdwQueryContainsStage(stage stuntgres.ForeignQueryStage, kind string) bool {
	if stage.Kind == kind { return true }
	for _, child := range stage.Children { if fdwQueryContainsStage(child,kind) { return true } }
	return false
}

func fdwExplainPlanningSentinel(run func(fixture.Step)) func() {
	run(fixture.Exec("CREATE SEQUENCE fdw_explain_planning_sequence",0))
	run(fixture.Exec("CREATE TABLE fdw_explain_planning_sentinel(id integer DEFAULT nextval('fdw_explain_planning_sequence'))",0))
	return func() {
		run(fixture.Select("SELECT id FROM fdw_explain_planning_sentinel",[]string{"id"},[]string{"INT4"},nil))
		run(fixture.Select("SELECT last_value,is_called FROM fdw_explain_planning_sequence",[]string{"last_value","is_called"},[]string{"INT8","BOOL"},[][]any{{int64(1),false}}))
	}
}

// Whole-input diagnostic positions legitimately differ by the EXPLAIN prefix.
// All executable structure, types, slots and literals remain exact.
func fdwQuerySemanticStage(stage stuntgres.ForeignQueryStage) stuntgres.ForeignQueryStage {
	var expression func(stuntgres.ForeignQueryExpression) stuntgres.ForeignQueryExpression
	expression = func(e stuntgres.ForeignQueryExpression) stuntgres.ForeignQueryExpression {
		e.Position = 0
		e.Arguments = append([]stuntgres.ForeignQueryExpression(nil),e.Arguments...)
		for i := range e.Arguments { e.Arguments[i] = expression(e.Arguments[i]) }
		return e
	}
	stage.Children = append([]stuntgres.ForeignQueryStage(nil),stage.Children...)
	for i := range stage.Children { stage.Children[i] = fdwQuerySemanticStage(stage.Children[i]) }
	stage.Outputs = append([]stuntgres.ForeignQueryExpression(nil),stage.Outputs...)
	for i := range stage.Outputs { stage.Outputs[i] = expression(stage.Outputs[i]) }
	if stage.Predicate != nil { value := expression(*stage.Predicate); stage.Predicate = &value }
	stage.Keys = append([]stuntgres.ForeignQueryExpression(nil),stage.Keys...)
	for i := range stage.Keys { stage.Keys[i] = expression(stage.Keys[i]) }
	stage.Order = append([]stuntgres.ForeignQueryOrder(nil),stage.Order...)
	for i := range stage.Order { stage.Order[i].Expression = expression(stage.Order[i].Expression) }
	stage.Aggregates = append([]stuntgres.ForeignQueryAggregate(nil),stage.Aggregates...)
	for i := range stage.Aggregates {
		stage.Aggregates[i].Arguments = append([]stuntgres.ForeignQueryExpression(nil),stage.Aggregates[i].Arguments...)
		for j := range stage.Aggregates[i].Arguments { stage.Aggregates[i].Arguments[j] = expression(stage.Aggregates[i].Arguments[j]) }
	}
	return stage
}
