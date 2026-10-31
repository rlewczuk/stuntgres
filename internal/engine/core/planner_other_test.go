package core_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/plan"
)

func TestPlannerOtherCursorAdmissionCapture(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	for _, q := range []string{"CREATE TABLE other_cursor_rows(n integer)", "INSERT INTO other_cursor_rows VALUES(2),(1),(2),(NULL),(3)", "CREATE INDEX other_cursor_hash ON other_cursor_rows USING hash(n)"} {
		enginetest.PlannerCostCommand(t, s, q)
	}
	for _, tc := range []struct {
		fraction string
		method   plan.ScanMethod
	}{{"0", plan.FullScan}, {"0.0001", plan.FullScan}, {"1", plan.HashCandidates}} {
		enginetest.PlannerCostCommand(t, s, "SET cursor_tuple_fraction='"+tc.fraction+"'")
		op, _, err := s.AcquireCommand(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		scope, err := e.StartStatement(op)
		if err != nil {
			t.Fatal(err)
		}
		_, err = scope.Execute(enginetest.Statement(t, "DECLARE other_choice NO SCROLL CURSOR WITH HOLD FOR SELECT n FROM other_cursor_rows WHERE n=2"), 0, nil)
		if err != nil {
			scope.Finish(err)
			op.Done()
			t.Fatal(err)
		}
		cursor := core.PayloadOf(s).Cursors.Entries["other_choice"]
		if cursor == nil || cursor.Source == nil || len(cursor.Source.HashIndexes) != 1 || cursor.Source.ScanDecision.Method != tc.method || cursor.Source.ScanDecision.FullCost != 12.5 {
			t.Fatal("declaration missed actual eligible decision", tc, cursor)
		}
		if tc.fraction == "0" && cursor.Source.ScanFraction != 1e-10 {
			t.Fatal(cursor.Source.ScanFraction)
		}
		if err = scope.Finish(nil); err != nil {
			t.Fatal(err)
		}
		op.Done()
		enginetest.PlannerCostCommand(t, s, "SET cursor_tuple_fraction='0.7'")
		enginetest.PlannerCostCommand(t, s, "FETCH ALL FROM other_choice")
		enginetest.PlannerCostCommand(t, s, "CLOSE other_choice")
	}
	// Explicit-transaction eligibility remains conservative. A live producer
	// captures once and later SET/FETCH must not replace its source or decision.
	enginetest.PlannerCostCommand(t, s, "BEGIN")
	enginetest.PlannerCostCommand(t, s, "SET LOCAL cursor_tuple_fraction='0.0001'")
	enginetest.PlannerCostCommand(t, s, "DECLARE other_live SCROLL CURSOR FOR SELECT n FROM other_cursor_rows WHERE n=2")
	cursor := core.PayloadOf(s).Cursors.Entries["other_live"]
	source, decision := cursor.Source, cursor.Source.ScanDecision
	enginetest.PlannerCostCommand(t, s, "SET LOCAL cursor_tuple_fraction=1")
	enginetest.PlannerCostCommand(t, s, "FETCH 1 FROM other_live")
	if cursor.Source != source || source.ScanDecision != decision || source.ScanFraction != .0001 {
		t.Fatal("FETCH replaced declaration admission")
	}
	enginetest.PlannerCostCommand(t, s, "CLOSE other_live")
	enginetest.PlannerCostCommand(t, s, "ROLLBACK")
}

func TestPlannerOtherRecursiveWorktableEstimate(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	const query = "WITH RECURSIVE a(n) AS (VALUES(1) UNION ALL SELECT n+1 FROM a WHERE n<3), b(n) AS (VALUES(1),(2) UNION ALL SELECT n+1 FROM b WHERE n<3) SELECT a.n FROM a JOIN b ON a.n=b.n"
	for _, factor := range []int{1, 10, 100, 1000000} {
		enginetest.PlannerCostCommand(t, s, fmt.Sprintf("SET recursive_worktable_factor='%d'", factor))
		r := enginetest.PlannerCostCommand(t, s, "EXPLAIN "+query)
		found := map[string]bool{}
		for _, row := range enginetest.Tuples(r) {
			line := row[0].(string)
			if strings.Contains(line, "NaN") || strings.Contains(line, "+Inf") {
				t.Fatal(line)
			}
			for _, tc := range []struct {
				name string
				seed int
			}{{"a", 1}, {"b", 2}} {
				if strings.Contains(line, "Worktable Scan on "+tc.name+" ") {
					if !strings.Contains(line, fmt.Sprintf("rows=%d logical", factor*tc.seed)) {
						t.Fatal("CTE identity lost", factor, line)
					}
					found[tc.name] = true
				}
			}
		}
		if len(found) != 2 {
			t.Fatal("missing independent self references", found)
		}
	}
	const nested = "WITH RECURSIVE outer_r(n) AS ((WITH RECURSIVE inner_r(n) AS (VALUES(1),(2) UNION ALL SELECT n+1 FROM inner_r WHERE n<2) SELECT n FROM inner_r) UNION ALL SELECT n+1 FROM outer_r WHERE n<3) SELECT n FROM outer_r"
	for _, factor := range []int{1, 10, 1000000} {
		enginetest.PlannerCostCommand(t, s, fmt.Sprintf("SET recursive_worktable_factor='%d'", factor))
		r := enginetest.PlannerCostCommand(t, s, "EXPLAIN "+nested)
		want := map[string]float64{"inner_r": 2 * float64(factor), "outer_r": min(plan.ExplainEstimateLimit, float64(factor)*(2+10*float64(factor)))}
		for _, row := range enginetest.Tuples(r) {
			line := row[0].(string)
			for name, rows := range want {
				if strings.Contains(line, "Worktable Scan on "+name+" ") {
					if !strings.Contains(line, fmt.Sprintf("rows=%.0f logical", rows)) {
						t.Fatal("nested worktable borrowed wrong seed/scope", factor, line, rows)
					}
					delete(want, name)
				}
			}
		}
		if len(want) != 0 {
			t.Fatal("missing nested estimates", want)
		}
	}
}
