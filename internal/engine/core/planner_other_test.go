package core_test

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/plan"
)

func TestPlannerOtherCursorAdmissionCapture(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	for _, q := range []string{"CREATE TABLE other_cursor_rows(n integer)", "INSERT INTO other_cursor_rows VALUES(2),(1),(2),(NULL),(3)", "CREATE INDEX other_cursor_hash ON other_cursor_rows USING hash(n)"} {
		plannerCostCommand(t, s, q)
	}
	for _, tc := range []struct {
		fraction string
		method   plan.ScanMethod
	}{{"0", plan.FullScan}, {"0.0001", plan.FullScan}, {"1", plan.HashCandidates}} {
		plannerCostCommand(t, s, "SET cursor_tuple_fraction='"+tc.fraction+"'")
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
		plannerCostCommand(t, s, "SET cursor_tuple_fraction='0.7'")
		plannerCostCommand(t, s, "FETCH ALL FROM other_choice")
		plannerCostCommand(t, s, "CLOSE other_choice")
	}
	// Explicit-transaction eligibility remains conservative. A live producer
	// captures once and later SET/FETCH must not replace its source or decision.
	plannerCostCommand(t, s, "BEGIN")
	plannerCostCommand(t, s, "SET LOCAL cursor_tuple_fraction='0.0001'")
	plannerCostCommand(t, s, "DECLARE other_live SCROLL CURSOR FOR SELECT n FROM other_cursor_rows WHERE n=2")
	cursor := core.PayloadOf(s).Cursors.Entries["other_live"]
	source, decision := cursor.Source, cursor.Source.ScanDecision
	plannerCostCommand(t, s, "SET LOCAL cursor_tuple_fraction=1")
	plannerCostCommand(t, s, "FETCH 1 FROM other_live")
	if cursor.Source != source || source.ScanDecision != decision || source.ScanFraction != .0001 {
		t.Fatal("FETCH replaced declaration admission")
	}
	plannerCostCommand(t, s, "CLOSE other_live")
	plannerCostCommand(t, s, "ROLLBACK")
}

func TestPlannerOtherConstraintEligibility(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	for _, q := range []string{
		"CREATE TABLE other_plain(n integer,CHECK(n>0))",
		"INSERT INTO other_plain VALUES(1),(NULL)",
		"CREATE TABLE other_parent(n integer)",
		"INSERT INTO other_parent VALUES(-1)",
		"CREATE TABLE other_child(CHECK(n>0)) INHERITS(other_parent)",
		"INSERT INTO other_child VALUES(1),(NULL)",
		"CREATE TABLE other_nn(n integer NOT NULL)",
		"INSERT INTO other_nn VALUES(1)",
		"CREATE TABLE other_nn_parent(n integer)",
		"INSERT INTO other_nn_parent VALUES(NULL)",
		"CREATE TABLE other_nn_child(n integer NOT NULL) INHERITS(other_nn_parent)",
		"INSERT INTO other_nn_child VALUES(1)",
		"CREATE TABLE other_part(n integer) PARTITION BY RANGE(n)",
		"CREATE TABLE other_positive PARTITION OF other_part FOR VALUES FROM(1) TO(10)",
		"INSERT INTO other_part VALUES(1)",
	} {
		plannerCostCommand(t, s, q)
	}
	for _, membership := range []bool{true, false} {
		plannerCostCommand(t, s, fmt.Sprintf("SET enable_partition_pruning=%t", membership))
		for _, mode := range []string{"on", "partition", "off"} {
			plannerCostCommand(t, s, "SET constraint_exclusion='"+mode+"'")
			for _, tc := range []struct {
				from, predicate string
				excluded        bool
				parent          string
			}{
				{"other_plain", "n<0", mode == "on", ""},
				{"ONLY other_plain", "n<0", mode == "on", ""},
				{"other_plain", "n IS NULL", false, ""},
				{"other_parent", "n<0", mode != "off", "other_parent"},
				{"other_parent", "n IS NULL", false, ""},
				{"other_child", "n<0", mode == "on", ""},
				{"ONLY other_child", "n<0", mode == "on", ""},
				{"ONLY other_child", "n IS NULL", false, ""},
				{"other_nn", "n IS NULL", mode == "on", ""},
				{"ONLY other_nn", "n IS NULL", mode == "on", ""},
				{"other_nn_parent", "n IS NULL", mode != "off", "other_nn_parent"},
				{"other_nn_child", "n IS NULL", mode == "on", ""},
				{"ONLY other_nn_child", "n IS NULL", mode == "on", ""},
				{"other_part", "n<0", membership, ""},
			} {
				op, _, err := s.AcquireCommand(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				query, source, release := hashCaptureQuery(t, e, op, "SELECT n FROM "+tc.from+" WHERE "+tc.predicate)
				project := query.Root.(*plan.ProjectNode)
				filter := project.Child.(*plan.FilterNode)
				state := exec.State{Source: source, Frame: project.Frame}
				evaluation := state.Evaluation(context.Background())
				selected, err := source.BranchPruner.Select(context.Background(), &evaluation, filter.Predicate, membership)
				want := exec.QueryBranchSelection{Applied: tc.excluded}
				if tc.excluded && tc.parent != "" {
					want.Intervals = []exec.QueryBranchInterval{{Start: 0, End: 1}}
					if len(source.Inheritance) != 2 || source.Inheritance[0].Leaf.Relation.Name() != tc.parent || !reflect.DeepEqual(source.InheritanceEnds, []int{1, source.Count()}) {
						t.Fatal("unexpected captured parent occurrence", tc, source.Inheritance, source.InheritanceEnds)
					}
				}
				if err != nil || !reflect.DeepEqual(selected, want) {
					t.Fatal(mode, membership, tc, selected, want, err)
				}
				stage := hashFilterStage(t, query, source, nil)
				if err := stage.Drain(context.Background()); err != nil {
					t.Fatal(err)
				}
				if tc.parent != "" && stage.Count() != 1 {
					t.Fatal("inheritance pruning changed exact parent occurrence", stage.Count())
				}
				stage.Close()
				release()
				op.Done()
			}
		}
	}
}

func TestPlannerOtherRecursiveWorktableEstimate(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	const query = "WITH RECURSIVE a(n) AS (VALUES(1) UNION ALL SELECT n+1 FROM a WHERE n<3), b(n) AS (VALUES(1),(2) UNION ALL SELECT n+1 FROM b WHERE n<3) SELECT a.n FROM a JOIN b ON a.n=b.n"
	for _, factor := range []int{1, 10, 100, 1000000} {
		plannerCostCommand(t, s, fmt.Sprintf("SET recursive_worktable_factor='%d'", factor))
		r := plannerCostCommand(t, s, "EXPLAIN "+query)
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
		plannerCostCommand(t, s, fmt.Sprintf("SET recursive_worktable_factor='%d'", factor))
		r := plannerCostCommand(t, s, "EXPLAIN "+nested)
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
