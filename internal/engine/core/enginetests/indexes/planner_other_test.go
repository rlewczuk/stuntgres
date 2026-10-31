package indexes_test

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/plan"
)

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
		enginetest.PlannerCostCommand(t, s, q)
	}
	for _, membership := range []bool{true, false} {
		enginetest.PlannerCostCommand(t, s, fmt.Sprintf("SET enable_partition_pruning=%t", membership))
		for _, mode := range []string{"on", "partition", "off"} {
			enginetest.PlannerCostCommand(t, s, "SET constraint_exclusion='"+mode+"'")
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
				query, source, release := enginetest.HashCaptureQuery(t, e, op, "SELECT n FROM "+tc.from+" WHERE "+tc.predicate)
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
