package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// The source is the existing independent five-row SQL fixture, not a request
// echo. Provider observations distinguish the selected executable representation.
func fdwExplainPlanningContexts(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step), unchanged func()) {
	t.Helper()
	run(fixture.Exec("CREATE VIEW fdw_context_count AS SELECT count(*) AS n FROM fdw_push_left", 0))
	for _, tc := range []struct {
		name, query string
		selected, enabled, excludeDiscovery bool
	}{
		{"top", "SELECT count(*) AS n FROM fdw_push_left", true, true, false},
		{"scalar", "SELECT (SELECT count(*) FROM fdw_push_left) AS n", false, true, true},
		{"derived", "SELECT n FROM (SELECT count(*) AS n FROM fdw_push_left) s", true, true, false},
		{"scalar-derived", "SELECT (SELECT n FROM (SELECT count(*) AS n FROM fdw_push_left) s) AS n", false, true, true},
		{"scalar-stored", "SELECT (SELECT n FROM fdw_context_count) AS n", true, true, false},
		{"cte-owner", "WITH counted AS MATERIALIZED (SELECT count(*) AS n FROM fdw_push_left) SELECT (SELECT n FROM counted) AS n", true, true, false},
		{"cte-folded", "WITH counted AS NOT MATERIALIZED (SELECT count(*) AS n FROM fdw_push_left) SELECT (SELECT n FROM counted) AS n", false, true, true},
		{"top-after", "SELECT count(*) AS n FROM fdw_push_left", true, true, false},
		{"declined", "SELECT count(*) AS n FROM fdw_push_left", false, false, false},
	} {
		r.mu.Lock(); r.queryPushdown = tc.enabled; r.queryIncompatible = false; r.mu.Unlock()
		explaining := true
		r.setHook(func(ctx context.Context, method string) error {
			if explaining && (method == "BeginForeignScan" || method == "BeginForeignQueryScan") { t.Errorf("%s EXPLAIN entered %s", tc.name, method) }
			if tc.excludeDiscovery && (method == "GetForeignJoinPaths" || method == "GetForeignUpperPaths") { return errors.New("nested execution context must not discover composite paths") }
			return ctx.Err()
		})
		before := r.snapshot()
		var selected stuntgres.ForeignExplainQueryPlan
		var base stuntgres.ForeignExplainScanRequest
		fdwExplainRoutes(t, p, c, "EXPLAIN (VERBOSE,COSTS false) "+tc.query, func(lines []string) {
			text := strings.Join(lines, "\n")
			if !strings.Contains(text, "Remote SQL:") { t.Fatal(tc.name, "missing genuine provider SQL", text) }
			if !b.PG {
				if r.snapshot().begins != before.begins { t.Fatal(tc.name, "EXPLAIN started a source scan") }
				r.advanced.mu.Lock()
				if tc.selected { selected = r.advanced.queryPlans[len(r.advanced.queryPlans)-1] } else { base = r.advanced.basePlans[len(r.advanced.basePlans)-1] }
				r.advanced.mu.Unlock()
				if tc.selected {
					if strings.Contains(text, "Foreign Scan on") || !fdwQueryContainsStage(selected.Request.Stage,"aggregate") { t.Fatal(tc.name,"lost selected upper",text) }
					fdwAssertPresenceQuery(t,selected.Request)
				} else {
					if !strings.Contains(text,"Foreign Scan on") || strings.Contains(text,"Foreign operation:") || !strings.Contains(text,"Remote SQL: SELECT 1 FROM") { t.Fatal(tc.name,"not a presence-only base fallback",text) }
					fdwAssertPresenceBase(t,base.ForeignRequest,base.Plan)
				}
			}
			unchanged()
		})
		explaining = false
		if !b.PG && tc.selected {
			r.queryRequestHook = func(actual stuntgres.ForeignQueryRequest) {
				fdwAssertPresenceQuery(t,actual)
				if !reflect.DeepEqual(fdwQuerySemanticStage(actual.Stage),fdwQuerySemanticStage(selected.Request.Stage)) || !reflect.DeepEqual(actual.Sources[0].Request.RequiredAttributes,selected.Request.Sources[0].Request.RequiredAttributes) { t.Error(tc.name,"explained and executed presence-only stages differ") }
			}
		}
		step := fixture.Select(tc.query,[]string{"n"},[]string{"INT8"},[][]any{{int64(5)}})
		run(step); run(fixture.Prepared(step)); fdwPushdownHeld(t,p,b,c,tc.query,step,step)
		if !b.PG {
			if tc.selected {
				r.advanced.mu.Lock()
				actual := r.advanced.executedPlans[len(r.advanced.executedPlans)-1]
				r.advanced.mu.Unlock()
				if !reflect.DeepEqual(actual,selected.Path.Plan) { t.Error(tc.name,"selected upper plan differs from execution",actual,selected.Path.Plan) }
			} else {
				r.advanced.mu.Lock()
				actual := r.advanced.baseExecutions[len(r.advanced.baseExecutions)-1]
				r.advanced.mu.Unlock()
				fdwAssertPresenceBase(t,actual.request,actual.plan)
				if !reflect.DeepEqual(actual.plan,base.Plan) || !reflect.DeepEqual(actual.request.RequiredAttributes,base.RequiredAttributes) { t.Error(tc.name,"selected base plan differs from execution",actual,base) }
			}
		}
		r.queryRequestHook = nil
		r.setHook(nil)
		unchanged()
	}
	r.mu.Lock(); r.queryPushdown = true; r.mu.Unlock()
}

func fdwAssertPresenceBase(t *testing.T, request stuntgres.ForeignRequest, plan stuntgres.ForeignPlan) {
	t.Helper()
	if len(request.Columns) != 2 || request.RequiredAttributes == nil || len(request.RequiredAttributes) != 0 || len(plan.Attributes) != 0 { t.Fatal("presence-only base demand became full projection",request,plan) }
}

func fdwAssertPresenceQuery(t *testing.T, request stuntgres.ForeignQueryRequest) {
	t.Helper()
	if len(request.Sources) != 1 || request.Sources[0].Request.RequiredAttributes == nil || len(request.Sources[0].Request.RequiredAttributes) != 0 { t.Fatal("presence-only composite source became full projection",request.Sources) }
	var source func(stuntgres.ForeignQueryStage) *stuntgres.ForeignQueryStage
	source = func(stage stuntgres.ForeignQueryStage) *stuntgres.ForeignQueryStage {
		if stage.Kind == "source" { return &stage }
		for _, child := range stage.Children { if found := source(child); found != nil { return found } }
		return nil
	}
	stage := source(request.Stage)
	if stage == nil || len(stage.Slots) < 2 || len(stage.Outputs) < 2 { t.Fatal("missing genuine source layout",stage) }
	for i := range 2 {
		if !stage.Slots[i].Unused || stage.Outputs[i].Kind != "literal" || stage.Outputs[i].Literal != nil || stage.Outputs[i].Type.TypeOID != 23 { t.Fatal("presence padding does not match declared unused INT4 slot",i,stage) }
	}
}

func fdwExplainContextLifecycle(t *testing.T) {
	fdwScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,r *fdwSQLReader,run func(fixture.Step)) {
		r.advanced = &fdwAdvancedState{}
		fdwPushdownLive(t,p,b,r,run)
		sentinel := fdwExplainPlanningSentinel(run)
		unchanged := func() {
			sentinel()
			fdwWriteSourceRows(t,p,b,r,fixture.Select(`SELECT "Key","Value" FROM fdw_source."Left input" ORDER BY "Key" NULLS LAST,"Value" NULLS LAST`,[]string{"Key","Value"},[]string{"INT4","INT4"},[][]any{{int64(1),int64(10)},{int64(1),nil},{int64(2),int64(20)},{int64(4),int64(40)},{nil,int64(30)}}))
			fdwWriteSourceRows(t,p,b,r,fixture.Select(`SELECT "Key","Weight" FROM fdw_source."Right input" ORDER BY "Key" NULLS LAST`,[]string{"Key","Weight"},[]string{"INT4","INT4"},[][]any{{int64(1),int64(100)},{int64(1),int64(100)},{int64(3),int64(300)},{nil,int64(900)}}))
		}
		for _, tc := range []struct{ query string; enabled bool }{
			{"SELECT count(*) AS n FROM fdw_push_left",true},
			{"SELECT (SELECT count(*) FROM fdw_push_left) AS n",true},
			{"SELECT count(*) AS n FROM fdw_push_left",false},
		} {
			r.mu.Lock(); r.queryPushdown = tc.enabled; r.mu.Unlock()
			explain := "EXPLAIN (VERBOSE,COSTS false) "+tc.query
			old := fdwExplainText(t,p,c,explain)
			saved := strings.Join(old,"\\n")
			unchanged()
			if !b.PG {
				phases := []string{"ExplainForeignScanResult"}
				if tc.enabled && !strings.Contains(tc.query,"(SELECT") { phases = append(phases,"GetForeignUpperPaths") }
				for _, phase := range phases {
					for _, action := range []string{"provider","cancel","mapping","role","grant","restore"} {
						fdwGatedPlanning(t,p,b,c,r,explain,phase,action,false,unchanged)
						fdwGatedPlanning(t,p,b,c,r,explain,phase,action,true,unchanged)
					}
				}
			}
			if strings.Join(old,"\\n") != saved { t.Fatal("saved presence-only plan TEXT changed") }
			fdwPushdownValue(run,tc.query,[]string{"n"},[]string{"INT8"},[][]any{{int64(5)}},false)
			unchanged()
		}
	})
}
