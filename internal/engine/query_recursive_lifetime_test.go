package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestQueryRecursiveCTELimitZeroAndEmptyAnchor(t *testing.T) {
	owner, state, node := recursiveUnitFixture(t, true, []int64{1}, 3)
	defer owner.Close()
	int8, _ := New().CatalogRead().LookupType(sqltype.Int8)
	zero, err := sqltype.Integer(int8, 0)
	if err != nil {
		t.Fatal(err)
	}
	limit := &plan.LimitNode{QuerySchema: node.Schema(), Child: node, Count: plan.LiteralExpr{Info: expr.Info{Usage: int8.Usage()}, Value: zero}}
	stage, err := exec.OpenLimitNode(context.Background(), state, limit)
	if err != nil {
		t.Fatal(err)
	}
	if stage.Count() != 0 || owner.Producers != nil || len(stage.Columns) != 1 {
		t.Fatal("LIMIT0 opened recursion or lost schema")
	}
	if err := stage.Close(); err != nil {
		t.Fatal(err)
	}
	emptyOwner, emptyState, emptyNode := recursiveUnitFixture(t, true, nil, 3)
	defer emptyOwner.Close()
	empty, err := exec.OpenCTEScan(context.Background(), emptyState, emptyNode)
	if err != nil {
		t.Fatal(err)
	}
	if _, present, err := empty.NextOccurrence(context.Background()); present || err != nil {
		t.Fatal(present, err)
	}
	runtime := emptyOwner.Producers[emptyNode.CTE].Recursion
	if runtime.Phase != 2 || runtime.Source.FlatCount != 0 || runtime.Opened {
		t.Fatal("empty anchor opened a recursive generation")
	}
}

func TestQueryRecursiveCTEPrimaryErrorAndRequiredClose(t *testing.T) {
	for _, primaryFailure := range []bool{false, true} {
		owner, state, node := recursiveUnitFixture(t, true, []int64{1}, 3)
		stage, err := exec.OpenCTEScan(context.Background(), state, node)
		if err != nil {
			t.Fatal(err)
		}
		cteUnitNext(t, &stage)
		producer := owner.Producers[node.CTE]
		cleanup := errors.New("required recursive child close failed")
		primary := errors.New("recursive term failed")
		closes := 0
		if primaryFailure {
			n := node.Query.Root.(*plan.ProjectNode).Child.(*plan.RecursiveUnionNode)
			n.RightExpressions = []expr.Expr{queryProbe(n.Schema()[0].Usage, func(*EvalContext) (sqltype.Value, error) {
				producer.Recursion.Arm.Inner.Finish = func() error { closes++; return cleanup }
				return sqltype.Value{}, primary
			})}
		} else {
			producer.Recursion.Arm.Inner.Finish = func() error { closes++; return cleanup }
			primary = cleanup
		}
		if _, _, err := stage.NextOccurrence(context.Background()); err != primary {
			t.Fatal("primary/close precedence", err, primary)
		}
		if err := owner.Close(); err != primary || closes != 1 {
			t.Fatal(err, closes)
		}
		if _, err := producer.Ensure(context.Background(), 0); err != primary {
			t.Fatal("failure not sticky on replay", err)
		}
		if err := owner.Close(); err != primary || closes != 1 {
			t.Fatal("closed recursive child twice", err, closes)
		}
	}
}

func TestQueryRecursiveCTELongIterationAndFreshInvocation(t *testing.T) {
	initial, base, node := recursiveUnitFixture(t, true, []int64{1}, 2000)
	defer initial.Close()
	for range 2 {
		stateCopy := *base
		owner := &cteInvocation{Frame: 1, State: stateCopy}
		stateCopy.Ctes = owner
		state := &stateCopy
		stage, err := exec.OpenCTEScan(context.Background(), state, node)
		if err != nil {
			t.Fatal(err)
		}
		for want := int64(1); want <= 2000; want++ {
			row := cteUnitNext(t, &stage)
			got, err := row[0].Integer()
			if err != nil || got != want {
				t.Fatal(got, want, err)
			}
		}
		if _, present, err := stage.NextOccurrence(context.Background()); present || err != nil {
			t.Fatal(present, err)
		}
		if err := owner.Close(); err != nil {
			t.Fatal(err)
		}
		if owner.Producers[node.CTE].Recursion.Seen != nil {
			t.Fatal("ALL retained seen state")
		}
	}
}
